package game

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDov serves a 629fm-shaped gallery of n distinct solid-colour PNGs, half
// archive rows and half uploads, plus a video row and a foreign src that must
// never be fetched. fetches counts image downloads.
func fakeDov(t *testing.T, n int) (srv *httptest.Server, fetches *atomic.Int32) {
	t.Helper()
	fetches = new(atomic.Int32)
	img := func(i int) []byte {
		m := image.NewNRGBA(image.Rect(0, 0, 40, 30))
		for p := 0; p < len(m.Pix); p += 4 {
			m.Pix[p], m.Pix[p+1], m.Pix[p+2], m.Pix[p+3] = uint8(i*5), uint8(255-i*5), uint8(i*37), 255
		}
		var buf bytes.Buffer
		png.Encode(&buf, m)
		return buf.Bytes()
	}
	type row struct {
		ID   int    `json:"id"`
		Kind string `json:"kind"`
		Mime string `json:"mime"`
		Src  string `json:"src"`
	}
	var rows []row
	files := map[string][]byte{}
	for i := range n {
		src := fmt.Sprintf("/archive/photos/dov/imgs/%03d.png", i)
		kind := "archive"
		if i%2 == 1 {
			src, kind = fmt.Sprintf("/api/dov/file/%08d-0000-4000-8000-000000000000.png", i), "upload"
		}
		rows = append(rows, row{ID: i, Kind: kind, Mime: "image/png", Src: src})
		files[src] = img(i)
	}
	rows = append(rows,
		row{ID: 900, Kind: "upload", Mime: "video/mp4", Src: "/api/dov/file/clip.mp4"},
		row{ID: 901, Kind: "archive", Mime: "image/png", Src: "http://elsewhere.invalid/x.png"},
	)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/dov/images" {
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "images": rows})
			return
		}
		data, ok := files[r.URL.Path]
		if !ok {
			t.Errorf("gallery fetched unexpected %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		fetches.Add(1)
		w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, fetches
}

// runDov starts the gallery loop against srv and waits for the first binding.
func runDov(t *testing.T, e *Engine, srv *httptest.Server, rotate time.Duration) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go e.RunDoV(ctx, DovConfig{APIURL: srv.URL, Poll: time.Hour, Rotate: rotate})
	waitFor(t, func() bool { return len(slotTextures(e)) > 0 })
}

// slotTextures is the texture every gallery slot currently shows.
func slotTextures(e *Engine) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []string
	for _, s := range e.dovSlots() {
		if tex := s.m.Grid[s.row][s.col].TextureName; strings.HasPrefix(tex, dovTexturePrefix) {
			out = append(out, tex)
		}
	}
	return out
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
	}
}

// pressSpace runs one tick with Space held, facing angle.
func pressSpace(e *Engine, p *Player, angle float64) {
	e.mu.Lock()
	p.Angle = angle
	e.mu.Unlock()
	e.Step(p, map[string]bool{" ": true}, 0)
	e.Step(p, map[string]bool{}, 0)
}

func TestPortalTravelsBetweenLevels(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	base := p.CurrentMap
	if base.Level != baseLevel || len(e.world.Maps) != 2 {
		t.Fatalf("maps = %d, base level %q", len(e.world.Maps), base.Level)
	}

	// the spawn room's north wall at (2,0) is the gallery portal
	p.X, p.Y = 2.5*TileSize, 1.5*TileSize
	pressSpace(e, p, -math.Pi/2)
	if p.CurrentMap.Level != "DOV_TILES.csv" {
		t.Fatalf("after Space on the portal: level %q", p.CurrentMap.Level)
	}
	// landed in front of the way back, facing into the room
	if c, r := int(p.X/TileSize), int(p.Y/TileSize); c != 7 || r != 8 || math.Abs(p.Angle+math.Pi/2) > 1e-9 {
		t.Fatalf("arrived at (%d,%d) angle %.2f, want (7,8) facing north", c, r, p.Angle)
	}

	pressSpace(e, p, math.Pi/2)
	if p.CurrentMap != base {
		t.Fatalf("return portal led to %q", p.CurrentMap.Level)
	}
	if c, r := int(p.X/TileSize), int(p.Y/TileSize); c != 2 || r != 1 {
		t.Fatalf("returned to (%d,%d), want (2,1) in front of the portal", c, r)
	}
}

func TestLoadContentRejectsMissingLevel(t *testing.T) {
	root := copyContent(t)
	if err := os.Remove(filepath.Join(root, "DOV_TILES.csv")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContent(root); err == nil || !strings.Contains(err.Error(), "DOV_TILES.csv") {
		t.Fatalf("LoadContent without the portal target: %v", err)
	}
}

func TestDovSlotsInWalkingOrder(t *testing.T) {
	e := newTestEngine(t)
	slots := e.dovSlots()
	if len(slots) != 41 {
		t.Fatalf("%d slots, want 41", len(slots))
	}
	// spawn (7,8) is in the south wall's middle: the slots beside the return
	// portal come first, the far north corners last
	if s := slots[0]; s.row != 9 || (s.col != 6 && s.col != 8) {
		t.Errorf("first slot (%d,%d), want next to the portal", s.col, s.row)
	}
	if s := slots[len(slots)-1]; s.row > 1 || (s.col > 1 && s.col < 13) {
		t.Errorf("last slot (%d,%d), want by a north corner", s.col, s.row)
	}
}

// TestDovGalleryRotates: 60 images through 41 slots — every slot fills, and
// the next rotation shows the following images.
func TestDovGalleryRotates(t *testing.T) {
	e := newTestEngine(t)
	srv, _ := fakeDov(t, 60)
	runDov(t, e, srv, 100*time.Millisecond)

	first := slotTextures(e)
	if len(first) != 41 {
		t.Fatalf("%d slots filled, want 41", len(first))
	}
	waitFor(t, func() bool { s := slotTextures(e); return len(s) > 0 && s[0] != first[0] })

	// the server only keeps textures for what's on the walls
	e.mu.RLock()
	defer e.mu.RUnlock()
	dovMap := e.levels["DOV_TILES.csv"]
	n := 0
	for key := range dovMap.Textures {
		if strings.HasPrefix(key, "picture:"+dovTexturePrefix) {
			n++
		}
	}
	if n > 41 {
		t.Errorf("%d gallery textures on the map, want at most 41", n)
	}
}

// TestDovUpdateReachesClient: a gallery change is a plain cell delta (no
// content reset, no snapshot, no pose), and the browser engine — fetching the
// composites through ContentHandler — draws exactly what the server does.
func TestDovUpdateReachesClient(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	p.X, p.Y = 2.5*TileSize, 1.5*TileSize
	pressSpace(e, p, -math.Pi/2) // into the gallery
	c := NewClient()
	_, raw := clientTick(t, e, p, ClientInput{})
	feedVia(t, e, c, raw)

	srv, _ := fakeDov(t, 10)
	runDov(t, e, srv, time.Hour)
	msg, raw := clientTick(t, e, p, ClientInput{})
	if msg.Content != nil || msg.Map != nil || msg.Pose != nil {
		t.Fatalf("gallery update resent content/map/pose: %+v", msg)
	}
	if len(msg.Cells) != 10 {
		t.Fatalf("%d cell deltas, want the 10 filled slots", len(msg.Cells))
	}
	feedVia(t, e, c, raw)

	for _, a := range []float64{math.Pi / 2, -math.Pi / 2, 0.4, math.Pi - 0.3} {
		p.Angle = a
		want := e.RenderFrame(p).Pix
		got := c.Render(p.X, p.Y, p.Angle, false, nil)
		if !bytes.Equal(got, want) {
			t.Errorf("angle %.2f: client frame differs from server", a)
		}
	}
}

// feedVia is feed, but gallery composites come from the engine's
// ContentHandler like in a browser.
func feedVia(t *testing.T, e *Engine, c *Client, raw []byte) {
	t.Helper()
	if err := c.Apply(raw); err != nil {
		t.Fatal(err)
	}
	h := e.ContentHandler("/content/")
	for {
		c.Render(0, 0, 0, false, nil)
		want := c.Wanted()
		if len(want) == 0 {
			return
		}
		for _, p := range want {
			var data []byte
			if strings.HasPrefix(p, dovFilePrefix) {
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, httptest.NewRequest("GET", "/content/"+p+"?v=whatever", nil))
				if rec.Code != 200 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
					t.Fatalf("%s: HTTP %d, Cache-Control %q", p, rec.Code, rec.Header().Get("Cache-Control"))
				}
				data = rec.Body.Bytes()
			} else {
				var err error
				if data, err = os.ReadFile(filepath.Join("../content", filepath.FromSlash(p))); err != nil {
					t.Fatal(err)
				}
			}
			c.AddFile(p, data)
		}
	}
}

func TestDovSpaceOpensOriginal(t *testing.T) {
	e := newTestEngine(t)
	srv, _ := fakeDov(t, 60)
	runDov(t, e, srv, time.Hour)
	p := e.Join(1, "tester", nil)
	e.mu.Lock()
	p.CurrentMap = e.levels["DOV_TILES.csv"]
	p.X, p.Y = 1.5*TileSize, 4.5*TileSize
	e.mu.Unlock()
	e.DrainEvents(p) // join music

	pressSpace(e, p, math.Pi) // the west wall slot at (0,4)
	evs := e.DrainEvents(p)
	if len(evs) != 1 || evs[0]["type"] != "picture" {
		t.Fatalf("events = %v, want one picture", evs)
	}
	src := evs[0]["src"].(string)
	if !strings.HasPrefix(src, "/dov/view/") {
		t.Fatalf("picture src %q", src)
	}

	rec := httptest.NewRecorder()
	e.DovViewHandler("/dov/view/").ServeHTTP(rec, httptest.NewRequest("GET", src, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("view: HTTP %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if _, err := png.Decode(rec.Body); err != nil {
		t.Fatalf("view is not the original PNG: %v", err)
	}

	rec = httptest.NewRecorder()
	e.DovViewHandler("/dov/view/").ServeHTTP(rec, httptest.NewRequest("GET", "/dov/view/nope", nil))
	if rec.Code != 404 {
		t.Errorf("unknown view: HTTP %d", rec.Code)
	}
}

func TestDovVacantSlotOpensNothing(t *testing.T) {
	e := newTestEngine(t) // no gallery loop: every slot is an empty frame
	p := e.Join(1, "tester", nil)
	e.mu.Lock()
	p.CurrentMap = e.levels["DOV_TILES.csv"]
	p.X, p.Y = 1.5*TileSize, 4.5*TileSize
	e.mu.Unlock()
	e.DrainEvents(p) // join music
	pressSpace(e, p, math.Pi)
	if evs := e.DrainEvents(p); len(evs) != 0 {
		t.Fatalf("vacant slot emitted %v", evs)
	}
}

// TestDovFailuresStayLocal: a gallery that goes away keeps the current
// images; a dead image leaves its slot empty without touching the others.
func TestDovFailuresStayLocal(t *testing.T) {
	e := newTestEngine(t)
	var down atomic.Bool
	inner, _ := fakeDov(t, 5)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() || strings.HasSuffix(r.URL.Path, "/002.png") {
			http.Error(w, "nope", http.StatusInternalServerError)
			return
		}
		inner.Config.Handler.ServeHTTP(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.RunDoV(ctx, DovConfig{APIURL: srv.URL, Poll: time.Millisecond, Rotate: 20 * time.Millisecond})
	waitFor(t, func() bool { return len(slotTextures(e)) == 4 })

	down.Store(true)
	time.Sleep(150 * time.Millisecond) // several failed polls
	if n := len(slotTextures(e)); n != 4 {
		t.Fatalf("after the gallery went down %d slots filled, want the 4 from before", n)
	}
}

func TestDovRejectsHugeImage(t *testing.T) {
	var buf bytes.Buffer
	png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	data := buf.Bytes()
	// rewrite IHDR to claim 20000x20000 (and fix its CRC): must be refused
	// from the header alone, before allocating 1.6 GB
	binary.BigEndian.PutUint32(data[16:], 20000)
	binary.BigEndian.PutUint32(data[20:], 20000)
	binary.BigEndian.PutUint32(data[29:], crc32.ChecksumIEEE(data[12:29]))
	if _, err := decodeDovImage(data); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("decodeDovImage(20000x20000) = %v", err)
	}
}

func TestDovSourceURL(t *testing.T) {
	cfg := DovConfig{APIURL: "http://api:3000/", WebURL: "http://web:8080"}
	for src, want := range map[string]string{
		"/api/dov/file/a.png":              "http://api:3000/api/dov/file/a.png",
		"/archive/photos/dov/imgs/001.png": "http://web:8080/archive/photos/dov/imgs/001.png",
		"http://elsewhere/x.png":           "",
		"/archive/photos/dov/../../etc":    "",
		"/etc/passwd":                      "",
	} {
		if got := cfg.sourceURL(src); got != want {
			t.Errorf("sourceURL(%q) = %q, want %q", src, got, want)
		}
	}
}
