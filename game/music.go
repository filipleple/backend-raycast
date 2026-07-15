package game

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MusicDef is one row of MUSIC_DEFS.csv: how a music zone ID sounds. The file
// is optional — unknown IDs fall back to /ost/<id>.wav, looping, full volume.
type MusicDef struct {
	File   string // URL the browser fetches; "" means silence
	Loop   bool
	Volume float64
	FadeMS int
}

// SilenceZone is the reserved "no music" zone ID.
const SilenceZone = "M0000"

// musicHysteresisTicks is how many consecutive ticks a player must stand in a
// new zone before the track switches — stops boundary strafing from spamming
// crossfades.
const musicHysteresisTicks = 3

func defaultMusicDef(id string) MusicDef {
	if id == SilenceZone || id == "" {
		return MusicDef{FadeMS: 1500}
	}
	return MusicDef{File: "/ost/" + id + ".wav", Loop: true, Volume: 1, FadeMS: 1500}
}

// loadMusicDefs parses MUSIC_DEFS.csv (id,file,loop,volume,fade_ms). Missing
// file is fine — everything falls back to defaults.
func loadMusicDefs(path string) (map[string]MusicDef, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]MusicDef{}, nil
		}
		return nil, err
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil || len(rows) < 1 {
		return nil, fmt.Errorf("music defs %s: %w", path, err)
	}
	col := map[string]int{}
	for i, name := range rows[0] {
		col[strings.TrimSpace(name)] = i
	}
	get := func(row []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[i])
	}

	defs := make(map[string]MusicDef, len(rows)-1)
	for _, row := range rows[1:] {
		id := get(row, "id")
		if id == "" {
			continue
		}
		d := defaultMusicDef(id)
		if file := get(row, "file"); file != "" {
			if strings.HasPrefix(file, "/") {
				d.File = file
			} else {
				d.File = "/ost/" + file
			}
		}
		if v := get(row, "loop"); v != "" {
			d.Loop = v == "1"
		}
		if v := get(row, "volume"); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				d.Volume = f
			}
		}
		if v := get(row, "fade_ms"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				d.FadeMS = n
			}
		}
		if id == SilenceZone {
			d.File = ""
		}
		defs[id] = d
	}
	return defs, nil
}

// loadMusicCSV parses the per-tile music-zone grid. It must match the tile
// map's dimensions; a missing file just disables zone music.
func loadMusicCSV(path string, cols, rows int) ([][]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	raw, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("music map %s: %w", path, err)
	}
	if len(raw) != rows {
		return nil, fmt.Errorf("music map %s: %d rows, tile map has %d", path, len(raw), rows)
	}
	zones := make([][]string, rows)
	for y := range raw {
		if len(raw[y]) < cols {
			return nil, fmt.Errorf("music map %s: row %d has %d cells, tile map has %d", path, y, len(raw[y]), cols)
		}
		zones[y] = make([]string, cols)
		for x := 0; x < cols; x++ {
			zones[y][x] = strings.TrimSpace(raw[y][x])
		}
	}
	return zones, nil
}

func (e *Engine) musicDef(id string) MusicDef {
	if d, ok := e.musicDefs[id]; ok {
		return d
	}
	return defaultMusicDef(id)
}

// setMusic switches p's track immediately and tells the browser. Caller must
// hold the write lock.
func (e *Engine) setMusic(p *Player, zone string) {
	p.musicZone = zone
	p.pendingZone = ""
	d := e.musicDef(zone)
	p.emit(Event{
		"type": "music", "id": zone,
		"file": d.File, "loop": d.Loop, "volume": d.Volume, "fade": d.FadeMS,
	})
}

// updateMusic runs each tick: looks up p's zone in MUSIC.csv and, after a
// short hysteresis, crossfades. Scripts can pin a track with MusicLock; zone
// logic resumes once unlocked. Caller must hold the write lock.
func (e *Engine) updateMusic(p *Player) {
	m := p.CurrentMap
	if m.MusicZones == nil || p.musicLocked {
		return
	}
	col, row := int(p.X/m.TileSize), int(p.Y/m.TileSize)
	if col < 0 || col >= m.Cols || row < 0 || row >= m.Rows {
		return
	}
	zone := m.MusicZones[row][col]
	if zone == p.musicZone {
		p.pendingZone = ""
		return
	}
	if zone != p.pendingZone {
		p.pendingZone, p.pendingTicks = zone, 0
	}
	p.pendingTicks++
	if p.pendingTicks >= musicHysteresisTicks {
		e.setMusic(p, zone)
	}
}
