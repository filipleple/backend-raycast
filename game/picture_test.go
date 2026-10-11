package game

import (
	"bytes"
	"image/png"
	"math"
	"strings"
	"testing"
)

func solidTexture(w, h int, r, g, b, a uint8) *Texture {
	t := &Texture{W: w, H: h, Pix: make([]uint8, w*h*4)}
	for i := 0; i < len(t.Pix); i += 4 {
		t.Pix[i], t.Pix[i+1], t.Pix[i+2], t.Pix[i+3] = r, g, b, a
	}
	return t
}

// TestComposePictureFillsTile: a padded frame must stretch edge to edge, and
// the picture must land inside the frame's window, never under its border.
func TestComposePictureFillsTile(t *testing.T) {
	// 100×100 frame: 10px transparent padding, 20px gold border, 40×40 window
	frame := solidTexture(100, 100, 0, 0, 0, 0)
	for y := 10; y < 90; y++ {
		for x := 10; x < 90; x++ {
			if x < 30 || x >= 70 || y < 30 || y >= 70 {
				i := (y*100 + x) * 4
				frame.Pix[i], frame.Pix[i+1], frame.Pix[i+2], frame.Pix[i+3] = 200, 160, 40, 255
			}
		}
	}
	pic := solidTexture(50, 50, 0, 0, 255, 255)
	out := composePicture(pic, frame)
	n := pictureTexSize
	if out.W != n || out.H != n {
		t.Fatalf("composite is %dx%d, want %dx%d", out.W, out.H, n, n)
	}
	isGold := func(x, y int) bool { r, g, b, _ := out.At(x, y); return r == 200 && g == 160 && b == 40 }
	isBlue := func(x, y int) bool { r, g, b, _ := out.At(x, y); return r == 0 && g == 0 && b == 255 }
	for _, p := range [][2]int{{0, 0}, {n - 1, 0}, {0, n - 1}, {n - 1, n - 1}, {0, n / 2}, {n / 2, 0}} {
		if !isGold(p[0], p[1]) {
			t.Errorf("tile edge %v is not frame — frame doesn't fill the tile", p)
		}
	}
	// window is the middle half after cropping the padding: [n/4, 3n/4)
	for _, p := range [][2]int{{n / 2, n / 2}, {n/4 + 2, n/4 + 2}, {3*n/4 - 3, 3*n/4 - 3}} {
		if !isBlue(p[0], p[1]) {
			t.Errorf("window point %v is not picture", p)
		}
	}
	for _, p := range [][2]int{{n/4 - 3, n / 2}, {n / 2, 3*n/4 + 2}} {
		if !isGold(p[0], p[1]) {
			t.Errorf("border point %v is not frame — picture bleeds out of the window", p)
		}
	}
}

func TestComposePictureWithoutFrame(t *testing.T) {
	pic := solidTexture(8, 8, 1, 2, 3, 255)
	if got := composePicture(pic, nil); got != pic {
		t.Fatal("missing frame should leave the bare picture")
	}
}

// TestSpaceOpensPicture: Space facing a picture cell emits a "picture" event
// whose URL the viewer endpoint can serve; facing away emits nothing.
func TestSpaceOpensPicture(t *testing.T) {
	e := newTestEngine(t)
	p := e.Join(1, "tester", nil)
	m := p.CurrentMap

	// find a picture cell with a walkable neighbour and stand there facing it
	var src string
	found := false
	for r := 0; r < m.Rows && !found; r++ {
		for c := 0; c < m.Cols && !found; c++ {
			if !m.Grid[r][c].Picture {
				continue
			}
			for _, d := range dirs {
				nc, nr := c+d[0], r+d[1]
				if nc < 0 || nc >= m.Cols || nr < 0 || nr >= m.Rows || !m.Grid[nr][nc].Walkable {
					continue
				}
				p.X, p.Y = (float64(nc)+0.5)*m.TileSize, (float64(nr)+0.5)*m.TileSize
				p.Angle = math.Atan2(float64(-d[1]), float64(-d[0]))
				src = pictureViewURL(&m.Grid[r][c])
				found = true
				break
			}
		}
	}
	if !found {
		t.Skip("content has no reachable picture cell")
	}

	_, events, err := e.Tick(p, map[string]bool{" ": true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(events), `"type":"picture"`) || !strings.Contains(string(events), src) {
		t.Fatalf("Space facing a picture emitted %s, want a picture event for %s", events, src)
	}

	// turn around: no picture event
	e.Tick(p, map[string]bool{}, 0)
	p.Angle += math.Pi
	if _, events, _ = e.Tick(p, map[string]bool{" ": true}, 0); strings.Contains(string(events), `"picture"`) {
		t.Fatalf("Space facing away emitted %s", events)
	}

	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(src, "/picture/"), ".png"), "/")
	data, err := e.PictureViewPNG(parts[0], parts[1])
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dy() != pictureViewH {
		t.Errorf("view is %v tall, want %d", img.Bounds().Dy(), pictureViewH)
	}
	if _, err := e.PictureViewPNG("../TILES", "x"); err == nil {
		t.Error("serving a pair no picture definition uses")
	}
}
