package game

import (
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
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

var textureSubdirs = [4]string{"walls", "doors", "floors", "misc"}

// loadTextureFile finds a texture by name, checking the preferred subdir
// first and falling back to the others (e.g. `grave` lives in misc/),
// .png before .gif.
func loadTextureFile(texturesDir, name, preferred string, keepAlpha bool) *Texture {
	subdirs := []string{preferred}
	for _, d := range textureSubdirs {
		if d != preferred {
			subdirs = append(subdirs, d)
		}
	}
	for _, subdir := range subdirs {
		for _, ext := range []string{".png", ".gif"} {
			f, err := os.Open(filepath.Join(texturesDir, subdir, name+ext))
			if err != nil {
				continue
			}
			img, _, err := image.Decode(f)
			f.Close()
			if err != nil {
				continue
			}
			return textureFromImage(img, keepAlpha)
		}
	}
	return nil
}

func loadImageFile(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
