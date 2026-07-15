package game

import (
	"encoding/csv"
	"fmt"
	"math"
	"os"
	"strings"
)

// Symbol is one map cell: ceiling / wall / floor layers resolved via
// definitions. Rendering keys off Wall (does the wall layer draw a pane?);
// movement keys off Walkable (are the wall AND floor layers passable?).
type Symbol struct {
	WallID    string
	FloorID   string
	CeilingID string

	TextureName  string // wall layer texture
	Transparency bool   // wall layer transparency
	Wall         bool   // wall layer draws a pane
	Door         bool   // wall layer is a door
	Walkable     bool   // wall walk_through AND floor walk_through

	FloorTexture   string // "" for solid walls / untextured
	CeilingTexture string
}

// Door is a same-map connectivity door — teleports between two exits.
type Door struct {
	Col, Row     int
	ExitA, ExitB [2]float64
}

// PortalDoor is a cross-map door. The target map is built lazily on first use.
type PortalDoor struct {
	Col, Row  int
	TargetMap *Map
	TargetPos [2]float64
}

var dirs = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}

// makeSymbol resolves a cell's three layer IDs into a Symbol through the
// definitions table — the single source of truth for both the map loader and
// scripted map edits.
func makeSymbol(defs map[string]Def, ceilingID, wallID, floorID string) Symbol {
	wallDef := defOr(defs, wallID)
	floorDef := defOr(defs, floorID)
	ceilingDef := defOr(defs, ceilingID)
	isSolid := wallDef.Wall && !wallDef.Transparency

	var floorTex, ceilingTex string
	if !isSolid {
		floorTex = floorDef.TextureName
		ceilingTex = ceilingDef.TextureName
		// a floor-type tile in the wall slot (water, rug, lava, ...)
		// draws no pane; it decorates the floor instead
		if wallDef.Floor && !wallDef.Wall && wallDef.TextureName != "" {
			floorTex = wallDef.TextureName
		}
	}

	return Symbol{
		WallID:         wallID,
		FloorID:        floorID,
		CeilingID:      ceilingID,
		TextureName:    wallDef.TextureName,
		Transparency:   wallDef.Transparency,
		Wall:           wallDef.Wall,
		Door:           wallDef.Door,
		Walkable:       wallDef.WalkThrough && floorDef.WalkThrough,
		FloorTexture:   floorTex,
		CeilingTexture: ceilingTex,
	}
}

// loadMapCSV parses the tile map using definitions. Each CSV cell is a quoted
// three-line value: ceiling, wall, floor tile IDs (top to bottom, matching
// what the player sees looking at the cell). Floor and ceiling textures are
// cleared for solid-opaque walls since those cells are never reached by
// floor/ceiling rays.
func loadMapCSV(path string, defs map[string]Def) (grid [][]Symbol, doorPositions [][2]int, cols, rows int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	raw, err := r.ReadAll()
	if err != nil {
		return nil, nil, 0, 0, fmt.Errorf("map %s: %w", path, err)
	}
	rows = len(raw)
	if rows == 0 {
		return nil, nil, 0, 0, fmt.Errorf("map %s: empty", path)
	}
	cols = len(raw[0])
	for i, v := range raw[0] {
		if strings.TrimSpace(v) == "" {
			cols = i
			break
		}
	}

	grid = make([][]Symbol, rows)
	for y := 0; y < rows; y++ {
		if len(raw[y]) < cols {
			return nil, nil, 0, 0, fmt.Errorf("map %s: row %d has %d cells, expected %d", path, y, len(raw[y]), cols)
		}
		grid[y] = make([]Symbol, cols)
		for x := 0; x < cols; x++ {
			layers := strings.Fields(raw[y][x])
			if len(layers) != 3 {
				return nil, nil, 0, 0, fmt.Errorf(
					"map %s: cell (%d,%d) has %d layer IDs, expected 3 (ceiling, wall, floor): %q",
					path, x, y, len(layers), raw[y][x])
			}
			sym := makeSymbol(defs, layers[0], layers[1], layers[2])
			if sym.Door {
				doorPositions = append(doorPositions, [2]int{x, y})
			}
			grid[y][x] = sym
		}
	}
	return grid, doorPositions, cols, rows, nil
}

// findSpawn returns the pixel (x, y) of the spawn tile (wall-layer id 0001),
// or failing that the walkable cell closest to grid origin.
func findSpawn(m *Map) (float64, float64) {
	bestC, bestR := -1, -1
	bestDist := math.Inf(1)
	for row := 0; row < m.Rows; row++ {
		for col := 0; col < m.Cols; col++ {
			sym := m.Grid[row][col]
			if sym.WallID == spawnID {
				return (float64(col) + 0.5) * m.TileSize, (float64(row) + 0.5) * m.TileSize
			}
			if sym.Walkable {
				if d := math.Hypot(float64(col), float64(row)); d < bestDist {
					bestDist, bestC, bestR = d, col, row
				}
			}
		}
	}
	if bestC < 0 {
		bestC, bestR = 0, 0
	}
	return (float64(bestC) + 0.5) * m.TileSize, (float64(bestR) + 0.5) * m.TileSize
}

// makeCSVDoors builds Door objects from cells marked door=1 in definitions,
// pairing the first two walkable neighbours as the teleport exits.
func makeCSVDoors(doorPositions [][2]int, grid [][]Symbol, cols, rows int, tileSize float64) map[[2]int]*Door {
	center := func(c, r int) [2]float64 {
		return [2]float64{(float64(c) + 0.5) * tileSize, (float64(r) + 0.5) * tileSize}
	}
	doorCells := map[[2]int]*Door{}
	for _, pos := range doorPositions {
		c, r := pos[0], pos[1]
		var exits [][2]int
		for _, d := range dirs {
			nc, nr := c+d[0], r+d[1]
			if nc >= 0 && nc < cols && nr >= 0 && nr < rows && grid[nr][nc].Walkable {
				exits = append(exits, [2]int{nc, nr})
			}
		}
		if len(exits) >= 2 {
			doorCells[pos] = &Door{
				Col: c, Row: r,
				ExitA: center(exits[0][0], exits[0][1]),
				ExitB: center(exits[1][0], exits[1][1]),
			}
		}
	}
	return doorCells
}
