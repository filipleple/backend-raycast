//go:build !js

package game

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"math"
	"net/url"
	"os"
)

// Pressing Space while facing a picture cell opens it full-screen in the
// browser: the engine emits a "picture" event carrying a URL, and the client
// fetches a high-resolution picture-in-frame PNG from PictureViewPNG. Unlike
// the square tile texture, the viewer keeps the picture's own aspect ratio and
// stretches the frame around it instead.

const (
	pictureViewH    = 1024 // viewer image height
	pictureViewMaxW = 2048
)

// pictureFrameName is the frame a picture definition renders with.
func pictureFrameName(d Def) string {
	if d.Frame == "" {
		return "default"
	}
	return d.Frame
}

// lookedAtPicture returns the picture cell straight ahead within reach, if the
// first wall along the look direction is one. Same sampling as doors.
func lookedAtPicture(m *Map, x, y, dirX, dirY float64) *Symbol {
	for _, t := range [...]float64{0.3, 0.5, 0.7, 1.0, 1.3} {
		c := int((x + dirX*m.TileSize*t) / m.TileSize)
		r := int((y + dirY*m.TileSize*t) / m.TileSize)
		if c < 0 || c >= m.Cols || r < 0 || r >= m.Rows {
			return nil
		}
		if cell := &m.Grid[r][c]; cell.Wall {
			if cell.Picture {
				return cell
			}
			return nil
		}
	}
	return nil
}

func pictureViewURL(sym *Symbol) string {
	return "/picture/" + url.PathEscape(sym.TextureName) + "/" + url.PathEscape(sym.FrameTexture) + ".png"
}

// PictureViewPNG renders the full-screen view of a picture/frame pair. Only
// pairs some picture definition actually uses are served. Results are cached
// until the next content reload.
func (e *Engine) PictureViewPNG(picture, frame string) ([]byte, error) {
	key := picture + "\x00" + frame
	e.mu.RLock()
	known := false
	for _, d := range e.defs {
		if d.Picture && d.TextureName == picture && pictureFrameName(d) == frame {
			known = true
			break
		}
	}
	cached, hit := e.pictureViews[key]
	e.mu.RUnlock()
	if !known {
		return nil, errors.New("no such picture")
	}
	if hit {
		return cached, nil
	}

	fsys := os.DirFS(e.root)
	pic := loadTextureFile(fsys, picture, "pictures", false)
	if pic == nil {
		return nil, errors.New("picture texture missing")
	}
	out := pic
	if fr := loadTextureFile(fsys, frame, "frames", true); fr != nil {
		out = composeView(pic, fr)
	}
	var buf bytes.Buffer
	img := &image.NRGBA{Pix: out.Pix, Stride: out.W * 4, Rect: image.Rect(0, 0, out.W, out.H)}
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}

	e.mu.Lock()
	e.pictureViews[key] = buf.Bytes()
	e.mu.Unlock()
	return buf.Bytes(), nil
}

// composeView sizes the frame so its window matches the picture's aspect
// ratio, then lays the picture into it.
func composeView(pic, frame *Texture) *Texture {
	bounds := opaqueBounds(frame, frameAlphaCut)
	// locate the window as a fraction of the frame on a cheap square probe
	n := pictureTexSize
	win := frameWindow(resampleArea(frame, bounds, n, n), frameAlphaCut)
	fw, fh := float64(win.Dx())/float64(n), float64(win.Dy())/float64(n)

	h := pictureViewH
	winH := fh * float64(h)
	winW := winH * float64(pic.W) / float64(pic.H)
	w := int(math.Round(winW / fw))
	w = min(max(w, 1), pictureViewMaxW)
	return composeFramed(pic, resampleArea(frame, bounds, w, h))
}
