package game

import (
	"bytes"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// frameSequences are deterministic input scripts, mirrored by the parity
// harness that drives the reference renderer.
var frameSequences = []struct {
	name  string
	ticks []map[string]bool
}{
	{"spawn", ticks(1, nil)},
	{"turnmove", append(ticks(10, keys("ArrowRight")), ticks(20, keys("ArrowUp"))...)},
	{"strafe", append(ticks(15, keys("a")), ticks(10, keys("ArrowLeft", "ArrowUp"))...)},
	{"minimap", ticks(1, keys("m"))},
}

func keys(names ...string) map[string]bool {
	m := map[string]bool{}
	for _, n := range names {
		m[n] = true
	}
	return m
}

func ticks(n int, k map[string]bool) []map[string]bool {
	out := make([]map[string]bool, n)
	for i := range out {
		if k == nil {
			out[i] = map[string]bool{}
		} else {
			out[i] = k
		}
	}
	return out
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	e, err := NewEngine("../content")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEngineTickProducesFrames(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	defer e.Leave(p)

	frame, _, err := e.Tick(p, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		t.Fatalf("tick output is not a decodable JPEG: %v", err)
	}
	if b := img.Bounds(); b.Dx() != ScreenW || b.Dy() != ScreenH {
		t.Fatalf("frame is %dx%d, want %dx%d", b.Dx(), b.Dy(), ScreenW, ScreenH)
	}

	turned, _, err := e.Tick(p, keys("ArrowRight"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(frame, turned) {
		t.Fatal("turning produced an identical frame")
	}
}

func TestPlayerCollidesWithWalls(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	defer e.Leave(p)

	m := p.CurrentMap
	for i := 0; i < 500; i++ {
		e.Step(p, keys("ArrowUp"))
		cx, cy := int(p.X/m.TileSize), int(p.Y/m.TileSize)
		if cx < 0 || cx >= m.Cols || cy < 0 || cy >= m.Rows {
			t.Fatalf("player escaped the map at tick %d: (%f, %f)", i, p.X, p.Y)
		}
		if !m.Grid[cy][cx].Walkable {
			t.Fatalf("player inside a non-walkable cell at tick %d: (%d, %d)", i, cx, cy)
		}
	}
}

// TestDumpFrames renders the shared sequences and writes PNGs for the parity
// comparison against the reference renderer. Gated on FRAME_DUMP_DIR.
func TestDumpFrames(t *testing.T) {
	dir := os.Getenv("FRAME_DUMP_DIR")
	if dir == "" {
		t.Skip("FRAME_DUMP_DIR not set")
	}

	for _, seq := range frameSequences {
		e := newTestEngine(t)
		p := e.Join(1, "tester", nil)
		for _, k := range seq.ticks {
			e.Step(p, k)
		}
		writePNG(t, filepath.Join(dir, "go_"+seq.name+".png"), e, p)
	}

	// two players: p2 idles at spawn, p1 backs away and looks at it
	e := newTestEngine(t)
	p1 := e.Join(1, "tester", nil)
	e.Join(2, "tester", nil)
	for i := 0; i < 5; i++ {
		e.Step(p1, keys("ArrowDown"))
	}
	writePNG(t, filepath.Join(dir, "go_sprite.png"), e, p1)
}

func writePNG(t *testing.T, path string, e *Engine, p *Player) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, e.RenderFrame(p)); err != nil {
		t.Fatal(err)
	}
}
