package game

import (
	"bytes"
	"encoding/json"
	"image"
	"os"
	"path/filepath"
	"sync"
)

// Screen and world constants. World scale is fixed, independent of screen and
// map size. Movement is in world pixels; speed + margin must stay below
// TileSize or the per-axis collision check can skip over a one-tile wall.
const (
	ScreenW = 320
	ScreenH = 240

	FOVDegrees = 60
	NumRays    = 120

	TileSize     = 64.0
	PlayerSpeed  = TileSize * 0.25 // world px per tick
	TurnSpeed    = 0.1
	PlayerMargin = TileSize * 0.2 // collision radius in world px

	jpegQuality = 60
)

type Monster struct {
	X, Y float64
}

// Map is a self-contained room.
type Map struct {
	Cols, Rows  int
	TileSize    float64
	Grid        [][]Symbol
	Textures    map[string]*Texture
	Monsters    []Monster
	DoorCells   map[[2]int]*Door
	PortalDoors map[[2]int]*PortalDoor
	MusicZones  [][]string // parallel MUSIC.csv grid; nil = no zone music
}

type World struct {
	Maps []*Map
}

type Player struct {
	ID         int
	Name       string
	Avatar     *Texture // nil falls back to hatman
	CurrentMap *Map
	X, Y       float64
	Angle      float64
	ShowMap    bool
	prevInputs map[string]bool

	events []Event // outgoing control messages, drained each tick

	// music-zone state machine
	musicZone    string
	musicLocked  bool // a script pinned the track; ignore zones
	pendingZone  string
	pendingTicks int

	// last cell, for onEnter edge detection
	prevCol, prevRow int
}

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
	musicDefs map[string]MusicDef
	scripts   *scriptHost
}

// NewEngine loads definitions, the tile map and all referenced textures from
// root (the directory holding definitions.csv, TILES.csv/map.csv, textures/
// and hatman.gif).
func NewEngine(root string) (*Engine, error) {
	defs, err := loadDefinitions(filepath.Join(root, "definitions.csv"))
	if err != nil {
		return nil, err
	}
	musicDefs, err := loadMusicDefs(filepath.Join(root, "MUSIC_DEFS.csv"))
	if err != nil {
		return nil, err
	}
	e := &Engine{root: root, defs: defs, musicDefs: musicDefs}

	m, err := e.buildMap()
	if err != nil {
		return nil, err
	}
	e.world = &World{Maps: []*Map{m}}
	e.scripts = newScriptHost(filepath.Join(root, "scripts"))

	sprite, err := loadImageFile(filepath.Join(root, "hatman.gif"))
	if err != nil {
		return nil, err
	}
	e.renderer = newRenderer(textureFromImage(sprite, true))
	return e, nil
}

// mapPath prefers the split-format TILES.csv and falls back to map.csv.
func (e *Engine) mapPath() string {
	tiles := filepath.Join(e.root, "TILES.csv")
	if _, err := os.Stat(tiles); err == nil {
		return tiles
	}
	return filepath.Join(e.root, "map.csv")
}

func (e *Engine) buildMap() (*Map, error) {
	grid, doorPositions, cols, rows, err := loadMapCSV(e.mapPath(), e.defs)
	if err != nil {
		return nil, err
	}
	m := &Map{
		Cols: cols, Rows: rows,
		TileSize:    TileSize,
		Grid:        grid,
		Textures:    map[string]*Texture{},
		DoorCells:   makeCSVDoors(doorPositions, grid, cols, rows, TileSize),
		PortalDoors: map[[2]int]*PortalDoor{},
	}
	zones, err := loadMusicCSV(filepath.Join(e.root, "MUSIC.csv"), cols, rows)
	if err != nil {
		return nil, err
	}
	m.MusicZones = zones

	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			e.ensureSymbolTextures(m, grid[r][c])
		}
	}
	return m, nil
}

// ensureTexture loads name into m.Textures if not already present.
func (e *Engine) ensureTexture(m *Map, name, preferred string, keepAlpha bool) {
	if name == "" {
		return
	}
	if _, ok := m.Textures[name]; ok {
		return
	}
	if t := loadTextureFile(filepath.Join(e.root, "textures"), name, preferred, keepAlpha); t != nil {
		m.Textures[name] = t
	}
}

// ensureSymbolTextures loads every texture sym references.
func (e *Engine) ensureSymbolTextures(m *Map, sym Symbol) {
	if sym.Wall && sym.TextureName != "" {
		preferred := "walls"
		if sym.Door {
			preferred = "door"
		}
		e.ensureTexture(m, sym.TextureName, preferred, sym.Transparency)
	}
	e.ensureTexture(m, sym.FloorTexture, "floors+ceilings", false)
	e.ensureTexture(m, sym.CeilingTexture, "floors+ceilings", false)
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
	e.ensureSymbolTextures(m, sym)

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
	if len(avatarBytes) > 0 {
		if img, _, err := image.Decode(bytes.NewReader(avatarBytes)); err == nil {
			avatar = textureFromImage(img, true)
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	p := &Player{
		ID:         id,
		Name:       name,
		Avatar:     avatar,
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

// Step advances p by one input snapshot.
func (e *Engine) Step(p *Player, keys map[string]bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.step(p, keys)
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
func (e *Engine) Tick(p *Player, keys map[string]bool) (frame, events []byte, err error) {
	e.Step(p, keys)
	if evs := e.DrainEvents(p); len(evs) > 0 {
		events, _ = json.Marshal(evs)
	}
	frame, err = encodeJPEG(e.RenderFrame(p))
	return frame, events, err
}
