//go:build !js

package game

import (
	"bytes"
	"encoding/json"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const jpegQuality = 60

// Engine owns the shared world and all connected players. Input steps mutate
// state under the write lock; rendering only reads and runs under the read
// lock, so sessions render concurrently.
type Engine struct {
	mu       sync.RWMutex
	world    *World
	players  []*Player
	renderer *renderer

	root      string
	defs      map[string]Def
	levels    map[string]*Map // every map by tiles CSV name, portal targets
	dov       *dovState       // Department of Vision gallery slots, see dov.go
	musicDefs map[string]MusicDef
	scripts   *scriptHost

	pictureViews map[string][]byte // PictureViewPNG cache, cleared on reload

	contentFiles   []string // fetchable by browser engines, see contentserve.go
	contentVersion string
}

// NewEngine loads all content under root (the directory holding
// definitions.csv, TILES.csv/map.csv, MUSIC*.csv, textures/, hatman.gif and
// scripts/) and assembles a ready-to-run engine. See LoadContent (content.go)
// for the loading + validation; ReloadContent hot-swaps it at runtime.
func NewEngine(root string) (*Engine, error) {
	c, err := LoadContent(root)
	if err != nil {
		return nil, err
	}
	e := &Engine{
		root:      root,
		defs:      c.defs,
		musicDefs: c.musicDefs,
		world:     &World{Maps: c.maps},
		levels:    c.levels(),
		renderer:  newRenderer(c.sprite),

		pictureViews: map[string][]byte{},

		contentFiles:   c.files,
		contentVersion: c.version,
	}
	e.scripts = newScriptHost(filepath.Join(root, "scripts"))
	e.dov = newDovState(root)
	return e, nil
}

// baseLevel is the map everyone spawns on.
const baseLevel = "TILES.csv"

// mapPath consumes the split-format TILES.csv
func mapPath(root, level string) string {
	tiles := filepath.Join(root, level)
	if _, err := os.Stat(tiles); err == nil || level != baseLevel {
		return tiles
	}
	return filepath.Join(root, "map.csv") // obsolete, will error, should never happen
}

// musicPath is a level's zone grid: TILES.csv -> MUSIC.csv, DOV_TILES.csv ->
// DOV_MUSIC.csv. Levels not named *TILES.csv have no zone music.
func musicPath(root, level string) string {
	stem, ok := strings.CutSuffix(level, "TILES.csv")
	if !ok {
		return ""
	}
	return filepath.Join(root, stem+"MUSIC.csv")
}

// buildMap loads one level's tile map + music zones under root, resolving
// cells through defs and loading every referenced texture. It is a free
// function (not an Engine method) so a content reload can build a map from
// freshly loaded defs before those defs are swapped onto the engine.
func buildMap(root string, defs map[string]Def, level string) (*Map, error) {
	grid, doorPositions, cols, rows, err := loadMapCSV(mapPath(root, level), defs)
	if err != nil {
		return nil, err
	}
	fsys := os.DirFS(root)
	m := &Map{
		Cols: cols, Rows: rows,
		TileSize:  TileSize,
		Grid:      grid,
		Textures:  map[string]*Texture{},
		DoorCells: makeCSVDoors(doorPositions, grid, cols, rows, TileSize),
		Level:     level,
	}
	if mp := musicPath(root, level); mp != "" {
		zones, err := loadMusicCSV(mp, cols, rows)
		if err != nil {
			return nil, err
		}
		m.MusicZones = zones
	}

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			ensureSymbolTextures(fsys, m, grid[r][c])
		}
	}
	return m, nil
}

// setCellLayer rewrites one layer of a cell through the definitions table,
// exactly like the map loader would, and keeps DoorCells coherent. The change
// is global — every player sees it. layer: 0 ceiling, 1 wall, 2 floor.
// Caller must hold the write lock.
func (e *Engine) setCellLayer(m *Map, col, row, layer int, id string) {
	if col < 0 || col >= m.Cols || row < 0 || row >= m.Rows {
		return
	}
	old := m.Grid[row][col]
	c, w, f := old.CeilingID, old.WallID, old.FloorID
	switch layer {
	case 0:
		c = id
	case 1:
		w = id
	case 2:
		f = id
	}
	sym := makeSymbol(e.defs, c, w, f)
	m.Grid[row][col] = sym
	ensureSymbolTextures(os.DirFS(e.root), m, sym)

	pos := [2]int{col, row}
	delete(m.DoorCells, pos)
	if sym.Door {
		for k, v := range makeCSVDoors([][2]int{pos}, m.Grid, m.Cols, m.Rows, m.TileSize) {
			m.DoorCells[k] = v
		}
	}
}

// Join spawns a player on the first map. avatarBytes may be nil or invalid
// image data; both fall back to the hatman sprite.
func (e *Engine) Join(id int, name string, avatarBytes []byte) *Player {
	var avatar *Texture
	var avatarRaw []byte
	if len(avatarBytes) > 0 {
		if img, _, err := image.Decode(bytes.NewReader(avatarBytes)); err == nil {
			avatar = textureFromImage(img, true)
			avatarRaw = avatarBytes
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	p := &Player{
		ID:         id,
		Name:       name,
		Avatar:     avatar,
		avatarRaw:  avatarRaw,
		CurrentMap: e.world.Maps[0],
		prevInputs: map[string]bool{},
	}
	p.X, p.Y = findSpawn(p.CurrentMap)
	p.prevCol, p.prevRow = int(p.X/p.CurrentMap.TileSize), int(p.Y/p.CurrentMap.TileSize)
	e.players = append(e.players, p)

	// start the spawn zone's track right away (no hysteresis on join)
	if z := p.CurrentMap.MusicZones; z != nil {
		e.setMusic(p, z[p.prevRow][p.prevCol])
	}
	e.scripts.fireJoin(e, p)
	return p
}

func (e *Engine) Leave(p *Player) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, q := range e.players {
		if q == p {
			e.players = append(e.players[:i], e.players[i+1:]...)
			break
		}
	}
}

// Step advances p by one input snapshot. turnDelta is accumulated mouse yaw
// (screen px) applied on top of arrow-key turning.
func (e *Engine) Step(p *Player, keys map[string]bool, turnDelta float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.step(p, keys, turnDelta)
}

// RenderFrame renders p's current view.
func (e *Engine) RenderFrame(p *Player) *image.NRGBA {
	e.mu.RLock()
	defer e.mu.RUnlock()
	others := make([]*Player, 0, len(e.players))
	for _, q := range e.players {
		if q != p {
			others = append(others, q)
		}
	}
	return e.renderer.render(p, others)
}

// Tick is one session tick: apply the input snapshot, render p's view and
// encode it for the wire. events is a JSON array of control messages for the
// browser, or nil when there is nothing to say this tick.
func (e *Engine) Tick(p *Player, keys map[string]bool, turnDelta float64) (frame, events []byte, err error) {
	e.Step(p, keys, turnDelta)
	if evs := e.DrainEvents(p); len(evs) > 0 {
		events, _ = json.Marshal(evs)
	}
	frame, err = encodeJPEG(e.RenderFrame(p))
	return frame, events, err
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
