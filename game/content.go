//go:build !js

package game

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// Content is a fully loaded, validated bundle of game content: tile
// definitions, music definitions, every level map (grid, zones, textures) and
// the player sprite. LoadContent parses it all from disk before any of it is made
// live, so a returned error means nothing changed — that makes LoadContent the
// validation gate for a hot content swap: a broken CSV never reaches players.
//
// Scripts are deliberately not part of Content — scriptHost owns its own
// hot-reload (see script.go) and ReloadContent just re-runs it.
type Content struct {
	defs      map[string]Def
	musicDefs map[string]MusicDef
	maps      []*Map // base level first, then every portal target by name
	sprite    *Texture

	files   []string // what browser engines may fetch (see contentserve.go)
	version string   // changes whenever any of files changes
}

// LoadContent reads and validates every content input under root:
// definitions.csv, MUSIC_DEFS.csv, TILES.csv (or map.csv), MUSIC.csv, every
// level a portal leads to (and its *MUSIC.csv), the textures/ tree and
// hatman.gif. Any parse error, dimension mismatch or missing
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
	// Build the maps from the freshly loaded defs — NOT any previously cached
	// engine defs — so edited definitions.csv actually takes effect.
	maps, err := buildLevels(root, defs)
	if err != nil {
		return nil, err
	}
	spriteImg, err := loadImageFile(os.DirFS(root), spriteFile)
	if err != nil {
		return nil, err
	}
	files, version, err := contentManifest(root)
	if err != nil {
		return nil, err
	}
	return &Content{
		defs:      defs,
		musicDefs: musicDefs,
		maps:      maps,
		sprite:    textureFromImage(spriteImg, true),
		files:     files,
		version:   version,
	}, nil
}

// buildLevels builds the base level and, transitively, every level a portal
// cell on it leads to. Levels are built eagerly so a broken or missing portal
// target fails content validation instead of a player's Space press.
func buildLevels(root string, defs map[string]Def) ([]*Map, error) {
	base, err := buildMap(root, defs, baseLevel)
	if err != nil {
		return nil, err
	}
	built := map[string]*Map{baseLevel: base}
	queue := []*Map{base}
	var names []string
	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]
		for _, row := range m.Grid {
			for _, sym := range row {
				target := sym.Portal
				if target == "" || built[target] != nil {
					continue
				}
				if filepath.Base(target) != target || filepath.Ext(target) != ".csv" {
					return nil, fmt.Errorf("portal tile %s: target %q is not a CSV file name", sym.WallID, target)
				}
				lm, err := buildMap(root, defs, target)
				if err != nil {
					return nil, fmt.Errorf("portal target %s: %w", target, err)
				}
				built[target] = lm
				names = append(names, target)
				queue = append(queue, lm)
			}
		}
	}
	// stable map IDs: base first, then by name
	slices.Sort(names)
	maps := []*Map{base}
	for _, n := range names {
		maps = append(maps, built[n])
	}
	for i, m := range maps {
		m.ID = i
	}
	return maps, nil
}

// levels indexes maps by level name.
func (c *Content) levels() map[string]*Map {
	out := make(map[string]*Map, len(c.maps))
	for _, m := range c.maps {
		out[m.Level] = m
	}
	return out
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
	e.world.Maps = c.maps
	e.levels = c.levels()
	e.renderer = newRenderer(c.sprite)
	e.scripts.reload()
	e.pictureViews = map[string][]byte{}
	e.contentFiles, e.contentVersion = c.files, c.version
	e.dov.reloadedLocked(e) // the fresh maps' gallery slots start vacant

	// Every live player caches a *Map pointer and an absolute pixel position
	// that a new grid can invalidate — a formerly walkable cell may now be a
	// wall (stranding them), the grid may be a different size, or their portal
	// target map no longer exists. Respawning everyone onto the fresh base map
	// is the deterministic reconciliation that sidesteps all of those.
	base := c.maps[0]
	sx, sy := findSpawn(base)
	for _, p := range e.players {
		p.CurrentMap = base
		p.X, p.Y = sx, sy
		p.prevCol = int(p.X / base.TileSize)
		p.prevRow = int(p.Y / base.TileSize)
		p.musicZone, p.pendingZone, p.pendingTicks = "", "", 0
		p.musicLocked = false
		p.emit(Event{"type": "popup", "text": "map reloaded", "ms": 2000})
		if z := base.MusicZones; z != nil {
			e.setMusic(p, z[p.prevRow][p.prevCol])
		}
	}
	return nil
}
