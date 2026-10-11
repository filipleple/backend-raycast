package game

import (
	"errors"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io/fs"
	"path"
)

// Texture is an RGBA pixel grid the renderer samples from. Textures marked
// opaque in definitions are flattened to alpha 255, mirroring the old
// renderer's RGB conversion.
type Texture struct {
	W, H int
	Pix  []uint8 // NRGBA order, 4 bytes per pixel, row-major
}

func textureFromImage(img image.Image, keepAlpha bool) *Texture {
	b := img.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(n, n.Bounds(), img, b.Min, draw.Src)
	if !keepAlpha {
		for i := 3; i < len(n.Pix); i += 4 {
			n.Pix[i] = 0xff
		}
	}
	return &Texture{W: b.Dx(), H: b.Dy(), Pix: n.Pix}
}

// At returns the NRGBA channels at (x, y). No bounds check.
func (t *Texture) At(x, y int) (r, g, b, a uint8) {
	i := (y*t.W + x) * 4
	return t.Pix[i], t.Pix[i+1], t.Pix[i+2], t.Pix[i+3]
}

var textureSubdirs = [6]string{"walls", "door", "floors", "misc", "pictures", "frames"}

// errPending is returned by a content filesystem for a file that exists but
// hasn't been downloaded yet (the browser engine fetches lazily). The loader
// stops probing on it instead of falling through to a lower-priority match,
// and the texture is retried once the file arrives.
var errPending = errors.New("content file not loaded yet")

// loadTextureFile finds a texture by name under textures/ in fsys, checking
// the preferred subdir first and falling back to the others (e.g. `grave`
// lives in misc/), .png before .gif. nil if missing or not downloaded yet.
func loadTextureFile(fsys fs.FS, name, preferred string, keepAlpha bool) *Texture {
	t, _ := loadTexture(fsys, name, preferred, keepAlpha)
	return t
}

// loadTexture is loadTextureFile reporting errPending, for callers that must
// not cache a partial result.
func loadTexture(fsys fs.FS, name, preferred string, keepAlpha bool) (*Texture, error) {
	subdirs := []string{preferred}
	for _, d := range textureSubdirs {
		if d != preferred {
			subdirs = append(subdirs, d)
		}
	}
	for _, subdir := range subdirs {
		for _, ext := range []string{".png", ".gif"} {
			f, err := fsys.Open(path.Join("textures", subdir, name+ext))
			if errors.Is(err, errPending) {
				return nil, err
			}
			if err != nil {
				continue
			}
			img, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				continue
			}
			return textureFromImage(img, keepAlpha), nil
		}
	}
	return nil, nil
}

func loadImageFile(fsys fs.FS, name string) (image.Image, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}

// ensureTexture loads name into m.Textures if not already present.
func ensureTexture(fsys fs.FS, m *Map, name, preferred string, keepAlpha bool) {
	if name == "" {
		return
	}
	if _, ok := m.Textures[name]; ok {
		return
	}
	if t := loadTextureFile(fsys, name, preferred, keepAlpha); t != nil {
		m.Textures[name] = t
	}
}

// ensurePictureTexture composes a picture cell's picture and frame into the
// single texture its pane is drawn with (see picture.go).
func ensurePictureTexture(fsys fs.FS, m *Map, sym Symbol) {
	key := sym.wallTextureKey()
	if _, ok := m.Textures[key]; ok {
		return
	}
	var pic, frame *Texture
	var picErr, frameErr error
	if sym.TextureName != "" {
		pic, picErr = loadTexture(fsys, sym.TextureName, "pictures", false)
	}
	if sym.FrameTexture != "" {
		// keep alpha: the frame's window and outer silhouette are transparent
		frame, frameErr = loadTexture(fsys, sym.FrameTexture, "frames", true)
	}
	if picErr != nil || frameErr != nil {
		return // half-downloaded: compose once both halves are here
	}
	if t := composePicture(pic, frame); t != nil {
		m.Textures[key] = t
	}
}

// ensureSymbolTextures loads every texture sym references.
func ensureSymbolTextures(fsys fs.FS, m *Map, sym Symbol) {
	switch {
	case sym.Wall && sym.Picture:
		ensurePictureTexture(fsys, m, sym)
	case sym.Wall && sym.TextureName != "":
		preferred := "walls"
		if sym.Door || sym.Portal != "" {
			preferred = "door"
		}
		ensureTexture(fsys, m, sym.TextureName, preferred, sym.Transparency)
	}
	ensureTexture(fsys, m, sym.FloorTexture, "floors+ceilings", false)
	ensureTexture(fsys, m, sym.CeilingTexture, "floors+ceilings", false)
}
