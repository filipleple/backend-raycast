package game

import (
	"bytes"
	"encoding/json"
	"image"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// clientTick runs one server tick for p and decodes the message.
func clientTick(t *testing.T, e *Engine, p *Player, in ClientInput) (ServerMsg, []byte) {
	t.Helper()
	raw, err := e.ClientTick(p, in)
	if err != nil {
		t.Fatal(err)
	}
	var msg ServerMsg
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatal(err)
	}
	return msg, raw
}

// feed applies a server message to c and satisfies its downloads straight
// from the content directory, like the page's fetch loop.
func feed(t *testing.T, c *Client, raw []byte) {
	t.Helper()
	if err := c.Apply(raw); err != nil {
		t.Fatal(err)
	}
	for {
		c.Render(0, 0, 0, false, nil) // resolving textures happens on draw too
		want := c.Wanted()
		if len(want) == 0 {
			return
		}
		for _, p := range want {
			data, err := os.ReadFile(filepath.Join("../content", filepath.FromSlash(p)))
			if err != nil {
				t.Fatal(err)
			}
			c.AddFile(p, data)
		}
	}
}

// TestClientRenderMatchesServer: the browser engine, fed only the wire
// protocol and downloaded files, draws exactly what the server renders.
func TestClientRenderMatchesServer(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	other := e.Join(2, "other", nil)
	c := NewClient()
	_, raw := clientTick(t, e, p, ClientInput{})
	feed(t, c, raw)

	// put the other player in view, then sweep a few poses incl. the minimap
	other.X, other.Y = p.X+80, p.Y
	msg, raw := clientTick(t, e, p, ClientInput{})
	feed(t, c, raw)
	var others []float64
	for _, o := range msg.Players {
		others = append(others, float64(o.ID), o.X, o.Y)
	}
	if len(others) != 3 {
		t.Fatalf("players = %+v, want the other player", msg.Players)
	}
	for i, a := range []float64{0, 1.3, math.Pi, -2.2} {
		p.Angle = a
		p.ShowMap = i == 3
		want := e.RenderFrame(p).Pix
		got := c.Render(p.X, p.Y, p.Angle, p.ShowMap, others)
		if !bytes.Equal(got, want) {
			diff := 0
			for j := range got {
				if got[j] != want[j] {
					diff++
				}
			}
			t.Errorf("angle %.2f: client frame differs from server in %d bytes", a, diff)
		}
	}
}

// TestClientPredictionMatchesServerStep: the browser's Step is the server's
// movement, so a key sequence ends in the same place on both.
func TestClientPredictionMatchesServerStep(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	c := NewClient()
	_, raw := clientTick(t, e, p, ClientInput{})
	feed(t, c, raw)

	x, y, a := p.X, p.Y, p.Angle
	for _, seq := range frameSequences {
		for _, k := range seq.ticks {
			if k[" "] || k["m"] {
				continue // Space/minimap are not prediction
			}
			e.Step(p, k, 0)
			x, y, a = c.Step(x, y, a, k)
			if x != p.X || y != p.Y || a != p.Angle {
				t.Fatalf("%s: client predicted (%v,%v,%v), server at (%v,%v,%v)", seq.name, x, y, a, p.X, p.Y, p.Angle)
			}
		}
	}
}

func TestClientMoveValidation(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	msg, _ := clientTick(t, e, p, ClientInput{})
	if msg.Pose == nil || msg.Content == nil || msg.Map == nil {
		t.Fatalf("first message lacks content/map/pose: %+v", msg)
	}
	epoch := msg.Pose.Epoch
	m := p.CurrentMap

	// find a walkable neighbour step
	var nx, ny float64
	for _, d := range dirs {
		x, y := p.X+float64(d[0])*PlayerSpeed, p.Y+float64(d[1])*PlayerSpeed
		if walkableCell(m, int(x/m.TileSize), int(y/m.TileSize)) {
			nx, ny = x, y
			break
		}
	}
	if nx == 0 {
		t.Skip("spawn is boxed in")
	}

	// a plausible step is accepted silently
	msg, _ = clientTick(t, e, p, ClientInput{Move: &ClientMsg{T: "move", X: nx, Y: ny, Angle: 0.5, Epoch: epoch}})
	if p.X != nx || p.Y != ny || p.Angle != 0.5 || msg.Pose != nil {
		t.Fatalf("plausible move: player at (%v,%v,%v), pose %+v", p.X, p.Y, p.Angle, msg.Pose)
	}

	// a teleport across the map is rejected and snapped back
	msg, _ = clientTick(t, e, p, ClientInput{Move: &ClientMsg{T: "move", X: nx + 20*TileSize, Y: ny, Angle: 0.5, Epoch: epoch}})
	if p.X != nx || msg.Pose == nil || msg.Pose.X != nx || msg.Pose.Epoch != epoch+1 {
		t.Fatalf("teleport move: player x %v, pose %+v", p.X, msg.Pose)
	}

	// a move stamped with the old epoch is ignored
	clientTick(t, e, p, ClientInput{Move: &ClientMsg{T: "move", X: p.X, Y: p.Y, Angle: 2, Epoch: epoch}})
	if p.Angle == 2 {
		t.Fatal("stale-epoch move was applied")
	}

	// a server-side teleport (script) produces a pose override
	p.X, p.Y = findSpawn(m)
	if msg, _ = clientTick(t, e, p, ClientInput{}); msg.Pose == nil || msg.Pose.X != p.X {
		t.Fatalf("server teleport sent pose %+v", msg.Pose)
	}
}

// TestClientCellDeltas: setCellLayer and scripts poking ctx.cell() directly
// both reach the browser as cell updates.
func TestClientCellDeltas(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	c := NewClient()
	_, raw := clientTick(t, e, p, ClientInput{})
	feed(t, c, raw)
	m := p.CurrentMap

	wc, wr := -1, -1 // some wall to knock out
	for r := 0; r < m.Rows && wc < 0; r++ {
		for col := 0; col < m.Cols; col++ {
			if m.Grid[r][col].Wall && m.Grid[r][col].WallID != "0000" && (col != 3 || r != 2) {
				wc, wr = col, r
				break
			}
		}
	}
	e.mu.Lock()
	e.setCellLayer(m, wc, wr, 1, "0000")
	m.Grid[2][3].Walkable = !m.Grid[2][3].Walkable // ctx.cell(3,2).walkable = ...
	e.mu.Unlock()

	msg, raw := clientTick(t, e, p, ClientInput{})
	if len(msg.Cells) != 2 {
		t.Fatalf("cells = %+v, want 2 changes", msg.Cells)
	}
	feed(t, c, raw)
	if c.m.Grid[wr][wc] != m.Grid[wr][wc] || c.m.Grid[2][3] != m.Grid[2][3] {
		t.Fatal("client grid did not follow the server")
	}
	if msg, _ = clientTick(t, e, p, ClientInput{}); len(msg.Cells) != 0 {
		t.Fatalf("unchanged tick resent cells %+v", msg.Cells)
	}
}

// TestClientUseDoor: Space at a connectivity door moves the player server
// side and the browser gets the new pose.
func TestClientUseDoor(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	msg, _ := clientTick(t, e, p, ClientInput{})
	m := p.CurrentMap
	for _, d := range m.DoorCells {
		p.X, p.Y = d.ExitA[0], d.ExitA[1]
		p.Angle = math.Atan2(float64(d.Row)*m.TileSize+m.TileSize/2-p.Y, float64(d.Col)*m.TileSize+m.TileSize/2-p.X)
		clientTick(t, e, p, ClientInput{}) // sync that pose
		msg, _ = clientTick(t, e, p, ClientInput{Use: true})
		if msg.Pose == nil || msg.Pose.X != d.ExitB[0] || msg.Pose.Y != d.ExitB[1] {
			t.Fatalf("door use sent pose %+v, want exit %v", msg.Pose, d.ExitB)
		}
		return
	}
	t.Skip("content has no connectivity doors")
}

func TestContentHandler(t *testing.T) {
	e := newTestEngine(t)
	h := e.ContentHandler("/content/")
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		return w
	}
	if w := get("/content/definitions.csv"); w.Code != 404 {
		t.Errorf("definitions.csv served: %d", w.Code)
	}
	if w := get("/content/scripts/blessed.js"); w.Code != 404 {
		t.Errorf("script served: %d", w.Code)
	}
	if w := get("/content/hatman.gif?v=" + e.contentVersion); w.Code != 200 || w.Header().Get("Cache-Control") == "no-cache" {
		t.Errorf("sprite: %d %q", w.Code, w.Header().Get("Cache-Control"))
	}
	w := get("/content/textures/frames/golden.png")
	if w.Code != 200 {
		t.Fatalf("frame: %d", w.Code)
	}
	cfg, _, err := image.DecodeConfig(w.Body)
	if err != nil || max(cfg.Width, cfg.Height) > maxServedPictureSide {
		t.Errorf("frame served at %dx%d (%v), want <= %d", cfg.Width, cfg.Height, err, maxServedPictureSide)
	}
}
