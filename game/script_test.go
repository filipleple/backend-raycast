package game

import "testing"

func drainTypes(t *testing.T, e *Engine, p *Player) map[string][]Event {
	t.Helper()
	out := map[string][]Event{}
	for _, ev := range e.DrainEvents(p) {
		typ, _ := ev["type"].(string)
		out[typ] = append(out[typ], ev)
	}
	return out
}

// TestJoinEmitsSpawnMusic verifies the join handshake starts the spawn
// zone's track immediately.
func TestJoinEmitsSpawnMusic(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	defer e.Leave(p)

	evs := drainTypes(t, e, p)
	if len(evs["music"]) == 0 {
		t.Fatal("no music event on join")
	}
	if id := evs["music"][0]["id"]; id != "M0001" {
		t.Fatalf("spawn music zone = %v, want M0001", id)
	}
}

// TestChapelZoneAndBlessing walks a player onto the chapel's 0003 trigger
// tile: the blessed.js script must fire (popup + tones) and, after the
// hysteresis window, the music must crossfade to the chapel zone M0002.
func TestChapelZoneAndBlessing(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	defer e.Leave(p)
	e.DrainEvents(p)

	// drop the player onto the nook-entrance trigger cell placed in TILES.csv
	const col, row = 55, 18
	p.X, p.Y = (col+0.5)*TileSize, (row+0.5)*TileSize

	e.Step(p, map[string]bool{}) // cell change detected here -> onEnter fires
	evs := drainTypes(t, e, p)
	if len(evs["popup"]) == 0 {
		t.Fatal("no popup after entering the trigger tile — blessed.js did not fire")
	}
	if len(evs["tone"]) == 0 {
		t.Fatal("no chime tones from blessed.js")
	}

	for i := 0; i < musicHysteresisTicks; i++ {
		e.Step(p, map[string]bool{})
	}
	evs = drainTypes(t, e, p)
	if len(evs["music"]) == 0 {
		t.Fatal("no music crossfade after standing in the chapel zone")
	}
	if id := evs["music"][0]["id"]; id != "M0002" {
		t.Fatalf("chapel music zone = %v, want M0002", id)
	}
}
