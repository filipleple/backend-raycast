package game

import "image"

// Picture cells render as one texture composed at load time: the frame,
// cropped to its visible pixels, stretched over the whole tile face; the
// picture scaled into the frame's window underneath it; and a dark backing
// behind the gaps in the frame's ornate outer silhouette. Composing once lets
// the (often huge) frame source be area-averaged down to screen resolution
// instead of point-sampled every frame.

const (
	pictureTexSize = 256 // composite side; a tile face is at most ScreenH tall
	frameAlphaCut  = 16  // alpha <= this counts as empty (drops PNG fringe noise)
)

var pictureBacking = [3]uint8{24, 18, 12}

// Department of Vision gallery slots (dov.go): a picture cell whose texture is
// dovSlotTexture is a slot. A filled slot's texture is dovTexturePrefix+name,
// a finished composite (no frame) served from memory under dovFilePrefix.
const (
	dovSlotTexture   = "dov"
	dovTexturePrefix = "dov/"
	dovFilePrefix    = "textures/pictures/" + dovTexturePrefix
)

// pictureTextureKey names a picture cell's composite in Map.Textures.
func pictureTextureKey(picture, frame string) string {
	return "picture:" + picture + "+" + frame
}

// wallTextureKey is the Map.Textures key the renderer draws a cell's pane with.
func (s *Symbol) wallTextureKey() string {
	if s.Picture {
		return pictureTextureKey(s.TextureName, s.FrameTexture)
	}
	return s.TextureName
}

// composePicture builds a picture cell's tile texture. A missing frame leaves
// the bare picture; a missing picture leaves the frame over its backing.
func composePicture(pic, frame *Texture) *Texture {
	if frame == nil {
		return pic
	}
	n := pictureTexSize
	return composeFramed(pic, resampleArea(frame, opaqueBounds(frame, frameAlphaCut), n, n))
}

// composeFramed lays pic into the window of f (an already-cropped frame at the
// output size) over the dark backing, then composites f on top.
func composeFramed(pic, f *Texture) *Texture {
	w, h := f.W, f.H
	out := &Texture{W: w, H: h, Pix: make([]uint8, w*h*4)}
	for i := 0; i < len(out.Pix); i += 4 {
		out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = pictureBacking[0], pictureBacking[1], pictureBacking[2], 0xff
	}
	if win := frameWindow(f, frameAlphaCut); pic != nil && !win.Empty() {
		p := resampleArea(pic, image.Rect(0, 0, pic.W, pic.H), win.Dx(), win.Dy())
		for y := 0; y < p.H; y++ {
			for x := 0; x < p.W; x++ {
				r, g, b, a := p.At(x, y)
				i := ((win.Min.Y+y)*w + win.Min.X + x) * 4
				blendPixel(out.Pix[i:i+4], r, g, b, a)
			}
		}
	}
	for i := 0; i < len(out.Pix); i += 4 {
		if a := f.Pix[i+3]; a > 0 {
			blendPixel(out.Pix[i:i+4], f.Pix[i], f.Pix[i+1], f.Pix[i+2], a)
		}
	}
	return out
}

// opaqueBounds is the bounding box of pixels with alpha > cut, or the whole
// texture if there are none.
func opaqueBounds(t *Texture, cut uint8) image.Rectangle {
	b := image.Rectangle{Min: image.Pt(t.W, t.H)}
	for y := 0; y < t.H; y++ {
		for x := 0; x < t.W; x++ {
			if t.Pix[(y*t.W+x)*4+3] > cut {
				b = b.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if b.Empty() {
		return image.Rect(0, 0, t.W, t.H)
	}
	return b
}

// frameWindow finds the frame's see-through window: the bounding box of the
// empty region connected to the center, grown by a pixel so the picture tucks
// under the frame's anti-aliased inner edge. A frame without an enclosed
// window (opaque center, or an empty region leaking to the border) gets the
// whole texture, i.e. the picture just sits behind the frame.
func frameWindow(t *Texture, cut uint8) image.Rectangle {
	full := image.Rect(0, 0, t.W, t.H)
	empty := func(x, y int) bool { return t.Pix[(y*t.W+x)*4+3] <= cut }
	cx, cy := t.W/2, t.H/2
	if !empty(cx, cy) {
		return full
	}
	seen := make([]bool, t.W*t.H)
	seen[cy*t.W+cx] = true
	stack := []image.Point{{cx, cy}}
	win := image.Rect(cx, cy, cx+1, cy+1)
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		win = win.Union(image.Rect(p.X, p.Y, p.X+1, p.Y+1))
		for _, d := range dirs {
			x, y := p.X+d[0], p.Y+d[1]
			if x < 0 || y < 0 || x >= t.W || y >= t.H {
				return full // leaks out of the frame: no enclosed window
			}
			if !seen[y*t.W+x] && empty(x, y) {
				seen[y*t.W+x] = true
				stack = append(stack, image.Pt(x, y))
			}
		}
	}
	return win.Inset(-1).Intersect(full)
}

// resampleArea scales the src region of t to w×h by box-averaging (alpha
// weighted, so transparent pixels don't darken edges). Upscaling degrades to
// nearest-neighbour.
func resampleArea(t *Texture, src image.Rectangle, w, h int) *Texture {
	out := &Texture{W: w, H: h, Pix: make([]uint8, w*h*4)}
	sw, sh := src.Dx(), src.Dy()
	for y := 0; y < h; y++ {
		y0 := src.Min.Y + y*sh/h
		y1 := max(src.Min.Y+(y+1)*sh/h, y0+1)
		for x := 0; x < w; x++ {
			x0 := src.Min.X + x*sw/w
			x1 := max(src.Min.X+(x+1)*sw/w, x0+1)
			var r, g, b, a, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					pr, pg, pb, pa := t.At(xx, yy)
					r += uint64(pr) * uint64(pa)
					g += uint64(pg) * uint64(pa)
					b += uint64(pb) * uint64(pa)
					a += uint64(pa)
					n++
				}
			}
			if a == 0 {
				continue
			}
			i := (y*w + x) * 4
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = uint8(r/a), uint8(g/a), uint8(b/a), uint8(a/n)
		}
	}
	return out
}
