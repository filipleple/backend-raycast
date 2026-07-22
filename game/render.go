package game

import (
	"bytes"
	"image"
	"image/jpeg"
	"math"
	"sort"
)

var (
	wallColor = [3]uint8{200, 200, 200} // grey
	paneColor = [3]uint8{255, 0, 0}     // red fallback for missing textures
	doorColor = [3]uint8{100, 60, 20}   // brown connectivity door on minimap
	dotColor  = [3]uint8{255, 0, 0}     // player dot on minimap
)

type renderer struct {
	width, height int
	hatman        *Texture
}

func newRenderer(hatman *Texture) *renderer {
	return &renderer{width: ScreenW, height: ScreenH, hatman: hatman}
}

// render draws p's view: floor/ceiling, wall panes, sprites, then the
// minimap overlay if toggled.
func (r *renderer) render(p *Player, others []*Player) *image.NRGBA {
	m := p.CurrentMap
	img := image.NewNRGBA(image.Rect(0, 0, r.width, r.height))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xff // opaque black canvas
	}

	r.renderFloorCeiling(img, p, m)

	hits := castFOV(m, p.X, p.Y, p.Angle)
	r.renderPanes(img, hits, m)

	// per-ray distance of the terminating (opaque or last) hit, for sprite
	// depth-testing
	distances := make([]float64, NumRays)
	for i, rayHits := range hits {
		if len(rayHits) > 0 {
			distances[i] = rayHits[len(rayHits)-1].Dist
		} else {
			distances[i] = math.Inf(1)
		}
	}
	r.renderSprites(img, p, m, distances, others)

	if p.ShowMap {
		r.drawWallMap(img, m, p)
	}
	return img
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// renderFloorCeiling casts a floor/ceiling ray per screen pixel: each row
// below (above) the horizon maps to a fixed world distance; walk the span
// between the leftmost and rightmost FOV rays and sample the tile texture
// under each point. The horizon row itself stays black.
func (r *renderer) renderFloorCeiling(img *image.NRGBA, p *Player, m *Map) {
	fov := float64(FOVDegrees) * math.Pi / 180
	projPlane := (float64(r.width) / 2) / math.Tan(fov/2)
	halfH := r.height / 2

	leftAngle := p.Angle - fov/2
	rightAngle := p.Angle + fov/2
	ldx, ldy := math.Cos(leftAngle), math.Sin(leftAngle)
	rdx, rdy := math.Cos(rightAngle), math.Sin(rightAngle)

	ts := m.TileSize

	paintRow := func(y int, ceiling bool) {
		ps := math.Abs(float64(y - halfH))
		rd := (ts * 0.5 * projPlane) / ps
		rowBase := y * img.Stride
		for x := 0; x < r.width; x++ {
			// ((d)*x)/width, not (d)*(x/width) — matches the reference
			// renderer's numpy evaluation order bit-for-bit
			wx := p.X + rd*(ldx+(rdx-ldx)*float64(x)/float64(r.width))
			wy := p.Y + rd*(ldy+(rdy-ldy)*float64(x)/float64(r.width))
			tx := int(wx / ts)
			ty := int(wy / ts)
			if tx < 0 || tx >= m.Cols || ty < 0 || ty >= m.Rows {
				continue
			}
			cell := &m.Grid[ty][tx]
			name := cell.FloorTexture
			if ceiling {
				name = cell.CeilingTexture
			}
			tex := m.Textures[name]
			if tex == nil {
				continue
			}
			u := posMod(wx/ts, 1.0)
			v := posMod(wy/ts, 1.0)
			px := int(u*float64(tex.W)) % tex.W
			py := int(v*float64(tex.H)) % tex.H
			cr, cg, cb, _ := tex.At(px, py)
			i := rowBase + x*4
			img.Pix[i], img.Pix[i+1], img.Pix[i+2] = cr, cg, cb
		}
	}

	for y := halfH + 1; y < r.height; y++ {
		paintRow(y, false)
	}
	for y := 0; y < halfH; y++ {
		paintRow(y, true)
	}
}

// renderPanes draws each ray's wall hits back-to-front as vertical texture
// strips with fisheye correction.
func (r *renderer) renderPanes(img *image.NRGBA, hits [][]rayHit, m *Map) {
	paneWidth := float64(r.width) / NumRays
	fov := float64(FOVDegrees) * math.Pi / 180
	projPlane := (float64(r.width) / 2) / math.Tan(fov/2)

	for i, rayHits := range hits {
		paneX := int(float64(i) * paneWidth)
		offset := (float64(i)/float64(NumRays-1) - 0.5) * fov

		for h := len(rayHits) - 1; h >= 0; h-- {
			hit := rayHits[h]
			dist := hit.Dist * math.Cos(offset)
			if dist <= 0.0001 || math.IsInf(dist, 0) {
				continue
			}

			paneHeight := math.Min((m.TileSize/dist)*projPlane, float64(r.height))
			y := int(float64(r.height)/2 - paneHeight/2)
			pw := int(paneWidth) + 1
			ph := int(paneHeight)
			if ph <= 0 {
				continue
			}

			cell := &m.Grid[hit.CY][hit.CX]
			tex := m.Textures[cell.TextureName]
			if tex == nil {
				drawRectOutline(img, paneX, y, paneX+pw, y+ph, paneColor)
				continue
			}

			texX := int(hit.U*float64(tex.W)) % tex.W
			r.drawTextureStrip(img, tex, texX, paneX, y, pw, ph, cell.Transparency)

			// a picture cell blits a golden frame overlay on top of the base
			// image; the frame's transparent center lets the picture show
			// through. Missing frame texture -> just the bare picture.
			if cell.Picture {
				if ftex := m.Textures[cell.FrameTexture]; ftex != nil {
					frameX := int(hit.U*float64(ftex.W)) % ftex.W
					r.drawTextureStrip(img, ftex, frameX, paneX, y, pw, ph, true)
				}
			}
		}
	}
}

// drawTextureStrip paints source column texX scaled to pw×ph at (paneX, y),
// nearest-neighbour, alpha-blending when the pane is transparent.
// The source row is accumulated by repeated addition, matching Pillow's
// incremental affine NEAREST exactly.
func (r *renderer) drawTextureStrip(img *image.NRGBA, tex *Texture, texX, paneX, y, pw, ph int, transparent bool) {
	scaleY := float64(tex.H) / float64(ph)
	yy := scaleY * 0.5
	for row := 0; row < ph; row++ {
		sy := y + row
		texY := int(yy)
		yy += scaleY
		if sy < 0 || sy >= r.height {
			continue
		}
		if texY >= tex.H {
			texY = tex.H - 1
		}
		cr, cg, cb, ca := tex.At(texX, texY)
		if transparent && ca == 0 {
			continue
		}
		for col := 0; col < pw; col++ {
			sx := paneX + col
			if sx < 0 || sx >= r.width {
				continue
			}
			i := img.PixOffset(sx, sy)
			if transparent && ca < 255 {
				blendPixel(img.Pix[i:i+4], cr, cg, cb, ca)
			} else {
				img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = cr, cg, cb, 0xff
			}
		}
	}
}

// renderSprites draws monsters and other players as camera-facing billboards,
// farthest first, depth-tested per column against the wall distances.
func (r *renderer) renderSprites(img *image.NRGBA, p *Player, m *Map, distances []float64, others []*Player) {
	fov := float64(FOVDegrees) * math.Pi / 180
	projPlane := (float64(r.width) / 2) / math.Tan(fov/2)

	type sprite struct {
		x, y, dist float64
		tex        *Texture
	}
	var sprites []sprite
	for _, mon := range m.Monsters {
		sprites = append(sprites, sprite{x: mon.X, y: mon.Y, tex: r.hatman})
	}
	for _, o := range others {
		if o.CurrentMap != m {
			continue
		}
		tex := o.Avatar
		if tex == nil {
			tex = r.hatman
		}
		sprites = append(sprites, sprite{x: o.X, y: o.Y, tex: tex})
	}
	for i := range sprites {
		sprites[i].dist = math.Hypot(sprites[i].x-p.X, sprites[i].y-p.Y)
	}
	sort.SliceStable(sprites, func(a, b int) bool { return sprites[a].dist > sprites[b].dist })

	for _, s := range sprites {
		if s.dist < 0.1 {
			continue
		}
		spriteAngle := math.Atan2(s.y-p.Y, s.x-p.X) - p.Angle
		spriteAngle = posMod(spriteAngle+math.Pi, 2*math.Pi) - math.Pi
		if math.Abs(spriteAngle) > fov/2+0.2 {
			continue
		}

		spriteH := int((m.TileSize / s.dist) * projPlane)
		if spriteH > r.height {
			spriteH = r.height
		}
		if spriteH < 1 {
			spriteH = 1
		}
		spriteW := spriteH
		screenX := int((spriteAngle/fov + 0.5) * float64(r.width))
		drawX := screenX - spriteW/2
		drawY := r.height/2 - spriteH/2

		// source coordinates accumulate by repeated addition, matching
		// Pillow's incremental affine NEAREST exactly
		scaleX := float64(s.tex.W) / float64(spriteW)
		scaleY := float64(s.tex.H) / float64(spriteH)

		xx := scaleX * 0.5
		for col := 0; col < spriteW; col++ {
			texX := int(xx)
			xx += scaleX
			screenCol := drawX + col
			if screenCol < 0 || screenCol >= r.width {
				continue
			}
			rayI := int(float64(screenCol) / float64(r.width) * NumRays)
			if rayI < 0 {
				rayI = 0
			}
			if rayI > NumRays-1 {
				rayI = NumRays - 1
			}
			offset := (float64(rayI)/float64(NumRays-1) - 0.5) * fov
			perpWall := distances[rayI] * math.Cos(offset)
			if s.dist >= perpWall {
				continue
			}

			if texX >= s.tex.W {
				texX = s.tex.W - 1
			}
			yy := scaleY * 0.5
			for row := 0; row < spriteH; row++ {
				sy := drawY + row
				texY := int(yy)
				yy += scaleY
				if sy < 0 || sy >= r.height {
					continue
				}
				if texY >= s.tex.H {
					texY = s.tex.H - 1
				}
				cr, cg, cb, ca := s.tex.At(texX, texY)
				if ca == 0 {
					continue
				}
				i := img.PixOffset(screenCol, sy)
				blendPixel(img.Pix[i:i+4], cr, cg, cb, ca)
			}
		}
	}
}

// drawWallMap overlays the minimap: wall cells grey, connectivity doors
// brown, the player a red dot. Minimap scale is screen-derived; world tile
// size stays fixed at 64.
func (r *renderer) drawWallMap(img *image.NRGBA, m *Map, p *Player) {
	scale := math.Min(float64(r.width)/float64(m.Cols), float64(r.height)/float64(m.Rows))
	for y := 0; y < m.Rows; y++ {
		for x := 0; x < m.Cols; x++ {
			if !m.Grid[y][x].Wall {
				continue
			}
			fill := wallColor
			if _, ok := m.DoorCells[[2]int{x, y}]; ok {
				fill = doorColor
			}
			fillRect(img,
				int(float64(x)*scale), int(float64(y)*scale),
				int(float64(x+1)*scale), int(float64(y+1)*scale), fill)
		}
	}
	px := p.X / m.TileSize * scale
	py := p.Y / m.TileSize * scale
	fillDot(img, px, py, dotColor)
}

// --- pixel helpers ---

// blendPixel source-over composites an NRGBA color onto an opaque dst pixel.
func blendPixel(dst []uint8, r, g, b, a uint8) {
	if a == 255 {
		dst[0], dst[1], dst[2], dst[3] = r, g, b, 255
		return
	}
	af := uint32(a)
	inv := 255 - af
	dst[0] = uint8((uint32(r)*af + uint32(dst[0])*inv) / 255)
	dst[1] = uint8((uint32(g)*af + uint32(dst[1])*inv) / 255)
	dst[2] = uint8((uint32(b)*af + uint32(dst[2])*inv) / 255)
	dst[3] = 255
}

func setPixel(img *image.NRGBA, x, y int, c [3]uint8) {
	if x < 0 || y < 0 || x >= img.Rect.Dx() || y >= img.Rect.Dy() {
		return
	}
	i := img.PixOffset(x, y)
	img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c[0], c[1], c[2], 0xff
}

// fillRect fills the rectangle inclusive of its end coordinates, like
// PIL's ImageDraw.rectangle.
func fillRect(img *image.NRGBA, x0, y0, x1, y1 int, c [3]uint8) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			setPixel(img, x, y, c)
		}
	}
}

func drawRectOutline(img *image.NRGBA, x0, y0, x1, y1 int, c [3]uint8) {
	for x := x0; x <= x1; x++ {
		setPixel(img, x, y0, c)
		setPixel(img, x, y1, c)
	}
	for y := y0; y <= y1; y++ {
		setPixel(img, x0, y, c)
		setPixel(img, x1, y, c)
	}
}

// fillDot draws the radius-2 player marker the way PIL rasterizes the
// equivalent ellipse: the 5×5 box minus its four corner pixels.
func fillDot(img *image.NRGBA, cx, cy float64, c [3]uint8) {
	x0, y0 := int(cx-2), int(cy-2)
	x1, y1 := int(cx+2), int(cy+2)
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			if (x == x0 || x == x1) && (y == y0 || y == y1) {
				continue
			}
			setPixel(img, x, y, c)
		}
	}
}
