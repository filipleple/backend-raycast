package game

import (
	"bytes"
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
}

type World struct {
	Maps []*Map
}

type Player struct {
	ID         int
	Avatar     *Texture // nil falls back to hatman
	CurrentMap *Map
	X, Y       float64
	Angle      float64
	ShowMap    bool
	prevInputs map[string]bool
}

// Engine owns the shared world and all connected players. Input steps mutate
// state under the write lock; rendering only reads and runs under the read
// lock, so sessions render concurrently.
type Engine struct {
	mu       sync.RWMutex
	world    *World
	players  []*Player
	renderer *renderer

	root string
	defs map[string]Def
}

// NewEngine loads definitions, the tile map and all referenced textures from
// root (the directory holding definitions.csv, TILES.csv/map.csv, textures/
// and hatman.gif).
func NewEngine(root string) (*Engine, error) {
	defs, err := loadDefinitions(filepath.Join(root, "definitions.csv"))
	if err != nil {
		return nil, err
	}
	e := &Engine{root: root, defs: defs}

	m, err := e.buildMap()
	if err != nil {
		return nil, err
	}
	e.world = &World{Maps: []*Map{m}}

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

	texDir := filepath.Join(e.root, "textures")
	loadTex := func(name, preferred string, keepAlpha bool) {
		if name == "" {
			return
		}
		if _, ok := m.Textures[name]; ok {
			return
		}
		if t := loadTextureFile(texDir, name, preferred, keepAlpha); t != nil {
			m.Textures[name] = t
		}
	}
	for r := 0; r < rows; r++ {
		for c := 0; c < cols; c++ {
			sym := grid[r][c]
			if sym.Wall && sym.TextureName != "" {
				preferred := "walls"
				if sym.Door {
					preferred = "door"
				}
				loadTex(sym.TextureName, preferred, sym.Transparency)
			}
			loadTex(sym.FloorTexture, "floors+ceilings", false)
			loadTex(sym.CeilingTexture, "floors+ceilings", false)
		}
	}
	return m, nil
}

// Join spawns a player on the first map. avatarBytes may be nil or invalid
// image data; both fall back to the hatman sprite.
func (e *Engine) Join(id int, avatarBytes []byte) *Player {
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
		Avatar:     avatar,
		CurrentMap: e.world.Maps[0],
		prevInputs: map[string]bool{},
	}
	p.X, p.Y = findSpawn(p.CurrentMap)
	e.players = append(e.players, p)
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
// encode it for the wire.
func (e *Engine) Tick(p *Player, keys map[string]bool) ([]byte, error) {
	e.Step(p, keys)
	return encodeJPEG(e.RenderFrame(p))
}
