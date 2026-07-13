package game

import "math"

// rayHit is one wall intersection along a ray: perpendicular distance in
// world px, the grid side hit (0 = vertical line, 1 = horizontal), the
// fractional position along the wall face (texture U) and the cell hit.
type rayHit struct {
	Dist   float64
	Side   int
	U      float64
	CX, CY int
}

// castRayDDA walks the grid from (ox, oy) along (dx, dy) and collects every
// pane the ray crosses, stopping at the first opaque wall. Transparent panes
// (arch, glass, sprite-like walls) accumulate so they can layer back-to-front.
func castRayDDA(m *Map, ox, oy, dx, dy float64) []rayHit {
	ts := m.TileSize
	mapX := int(math.Floor(ox / ts))
	mapY := int(math.Floor(oy / ts))

	// bail only when the camera sits inside a solid-opaque wall; transparent
	// or walk-through wall layers still get cast through
	if mapX >= 0 && mapX < m.Cols && mapY >= 0 && mapY < m.Rows {
		start := m.Grid[mapY][mapX]
		if start.Wall && !start.Transparency {
			return nil
		}
	}

	stepX, stepY := 0, 0
	sideDistX, sideDistY := math.Inf(1), math.Inf(1)
	deltaDistX, deltaDistY := math.Inf(1), math.Inf(1)

	if dx != 0 {
		deltaDistX = math.Abs(ts / dx)
		if dx < 0 {
			stepX = -1
			sideDistX = (ox - float64(mapX)*ts) / math.Abs(dx)
		} else {
			stepX = 1
			sideDistX = (float64(mapX+1)*ts - ox) / math.Abs(dx)
		}
	}
	if dy != 0 {
		deltaDistY = math.Abs(ts / dy)
		if dy < 0 {
			stepY = -1
			sideDistY = (oy - float64(mapY)*ts) / math.Abs(dy)
		} else {
			stepY = 1
			sideDistY = (float64(mapY+1)*ts - oy) / math.Abs(dy)
		}
	}

	var hits []rayHit
	for {
		var side int
		if sideDistX < sideDistY {
			sideDistX += deltaDistX
			mapX += stepX
			side = 0
		} else {
			sideDistY += deltaDistY
			mapY += stepY
			side = 1
		}

		if mapX < 0 || mapX >= m.Cols || mapY < 0 || mapY >= m.Rows {
			break
		}

		cell := m.Grid[mapY][mapX]
		if cell.Wall {
			var dist float64
			if side == 0 {
				dist = sideDistX - deltaDistX
			} else {
				dist = sideDistY - deltaDistY
			}
			// UV: fractional position along the wall face that was hit
			var u float64
			if side == 0 {
				u = posMod((oy+dist*dy)/ts, 1.0)
			} else {
				u = posMod((ox+dist*dx)/ts, 1.0)
			}
			hits = append(hits, rayHit{Dist: dist, Side: side, U: u, CX: mapX, CY: mapY})
			if !cell.Transparency {
				break
			}
		}
	}
	return hits
}

// posMod is Python's %: the result carries the sign of the divisor.
func posMod(a, b float64) float64 {
	r := math.Mod(a, b)
	if r < 0 {
		r += b
	}
	return r
}
