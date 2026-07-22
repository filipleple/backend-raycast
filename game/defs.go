package game

import (
	"encoding/csv"
	"fmt"
	"os"
)

// Def is one row of definitions.csv: how a tile ID behaves in any layer.
type Def struct {
	TextureName  string
	Transparency bool
	WalkThrough  bool
	Wall         bool
	Floor        bool
	Door         bool
	Picture      bool   // wall face is a framed picture
	Frame        string // frame-overlay texture name ("" -> default frame)
}

// fallbackDef is used for IDs missing from definitions.csv: invisible,
// walkable, draws no pane.
var fallbackDef = Def{Transparency: true, WalkThrough: true}

const spawnID = "0001"

func loadDefinitions(path string) (map[string]Def, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// tolerate ragged rows: the optional picture/frame columns may be absent
	// from older rows or an out-of-date sheet (see cell() below)
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("definitions %s: %w", path, err)
	}
	if len(rows) < 1 {
		return nil, fmt.Errorf("definitions %s: empty", path)
	}

	col := map[string]int{}
	for i, name := range rows[0] {
		col[name] = i
	}
	for _, want := range []string{"id", "texture_name", "transparency", "walk_through", "wall", "floor", "door"} {
		if _, ok := col[want]; !ok {
			return nil, fmt.Errorf("definitions %s: missing column %q", path, want)
		}
	}

	// picture/frame are optional columns — an out-of-date sheet without them
	// still loads (defaulting to non-picture tiles).
	cell := func(row []string, name string) string {
		if i, ok := col[name]; ok && i < len(row) {
			return row[i]
		}
		return ""
	}

	defs := make(map[string]Def, len(rows)-1)
	for _, row := range rows[1:] {
		defs[row[col["id"]]] = Def{
			TextureName:  row[col["texture_name"]],
			Transparency: row[col["transparency"]] == "1",
			WalkThrough:  row[col["walk_through"]] == "1",
			Wall:         row[col["wall"]] == "1",
			Floor:        row[col["floor"]] == "1",
			Door:         row[col["door"]] == "1",
			Picture:      cell(row, "picture") == "1",
			Frame:        cell(row, "frame"),
		}
	}
	return defs, nil
}

func defOr(defs map[string]Def, id string) Def {
	if d, ok := defs[id]; ok {
		return d
	}
	return fallbackDef
}
