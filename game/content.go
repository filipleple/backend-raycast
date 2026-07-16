package game

import "path/filepath"

// Content is a fully loaded, validated bundle of game content: tile
// definitions, music definitions, the base map (grid, zones, textures) and the
// player sprite. LoadContent parses it all from disk before any of it is made
// live, so a returned error means nothing changed — that makes LoadContent the
// validation gate for a hot content swap: a broken CSV never reaches players.
//
// Scripts are deliberately not part of Content — scriptHost owns its own
// hot-reload (see script.go) and ReloadContent just re-runs it.
type Content struct {
	defs      map[string]Def
	musicDefs map[string]MusicDef
	baseMap   *Map
	sprite    *Texture
}

// LoadContent reads and validates every content input under root:
// definitions.csv, MUSIC_DEFS.csv, TILES.csv (or map.csv), MUSIC.csv, the
// textures/ tree and hatman.gif. Any parse error, dimension mismatch or missing
// required file is returned with no side effects.
func LoadContent(root string) (*Content, error) {
	defs, err := loadDefinitions(filepath.Join(root, "definitions.csv"))
	if err != nil {
		return nil, err
	}
	musicDefs, err := loadMusicDefs(filepath.Join(root, "MUSIC_DEFS.csv"))
	if err != nil {
		return nil, err
	}
	// Build the map from the freshly loaded defs — NOT any previously cached
	// engine defs — so edited definitions.csv actually takes effect.
	baseMap, err := buildMap(root, defs)
	if err != nil {
		return nil, err
	}
	spriteImg, err := loadImageFile(filepath.Join(root, "hatman.gif"))
	if err != nil {
		return nil, err
	}
	return &Content{
		defs:      defs,
		musicDefs: musicDefs,
		baseMap:   baseMap,
		sprite:    textureFromImage(spriteImg, true),
	}, nil
}

// ReloadContent re-reads all content from disk and, if it validates, swaps it
// into the running engine atomically and re-points every live player. On any
// load error the current content is left untouched and the error is returned —
// so an admin sync can call this and surface the parse failure without ever
// disturbing connected players.
//
// Unlike step(), this runs from outside the tick loop (an admin HTTP handler),
// so it takes the write lock itself. The heavy IO + validation happens BEFORE
// the lock; only the pointer swaps happen under it.
func (e *Engine) ReloadContent() error {
	c, err := LoadContent(e.root)
	if err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	e.defs = c.defs
	e.musicDefs = c.musicDefs
	e.world.Maps = []*Map{c.baseMap} // drop any lazily built portal maps
	e.renderer = newRenderer(c.sprite)
	e.scripts.reload()

	// Every live player caches a *Map pointer and an absolute pixel position
	// that a new grid can invalidate — a formerly walkable cell may now be a
	// wall (stranding them), the grid may be a different size, or their portal
	// target map no longer exists. Respawning everyone onto the fresh base map
	// is the deterministic reconciliation that sidesteps all of those.
	sx, sy := findSpawn(c.baseMap)
	for _, p := range e.players {
		p.CurrentMap = c.baseMap
		p.X, p.Y = sx, sy
		p.prevCol = int(p.X / c.baseMap.TileSize)
		p.prevRow = int(p.Y / c.baseMap.TileSize)
		p.musicZone, p.pendingZone, p.pendingTicks = "", "", 0
		p.musicLocked = false
		p.emit(Event{"type": "popup", "text": "map reloaded", "ms": 2000})
		if z := c.baseMap.MusicZones; z != nil {
			e.setMusic(p, z[p.prevRow][p.prevCol])
		}
	}
	return nil
}
