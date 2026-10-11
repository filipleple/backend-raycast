//go:build !js

package game

import "math"

// step applies one tick of input to p. Caller must hold the write lock.
// turnDelta is accumulated mouse movementX for this tick (screen px).
func (e *Engine) step(p *Player, inputs map[string]bool, turnDelta float64) {
	e.scripts.maybeReload()

	p.Angle = turnAngle(p.Angle, inputs, turnDelta)
	dirX, dirY := math.Cos(p.Angle), math.Sin(p.Angle)
	// the move vector is fixed before Space handling: a script may turn the
	// player in onUse, but this tick's movement keeps the pre-script heading
	moveX, moveY := moveVector(p.Angle, inputs)

	if inputs["m"] && !p.prevInputs["m"] {
		p.ShowMap = !p.ShowMap
	}

	if inputs[" "] && !p.prevInputs[" "] {
		e.use(p, dirX, dirY)
	}

	// collision uses CurrentMap, which may have just changed via portal
	p.X, p.Y = collide(p.CurrentMap, p.X, p.Y, moveX, moveY)
	p.prevInputs = inputs

	e.afterMove(p)
}

// use is Space: same-map doors, script onUse handlers, the picture viewer and
// cross-map portals, sampled along the look direction.
func (e *Engine) use(p *Player, dirX, dirY float64) {
	m := p.CurrentMap
	// sample along look direction at multiple depths — robust to approach
	// angle and exact distance from the door
	var connDoor *Door
	for _, t := range [...]float64{0.3, 0.5, 0.7, 1.0, 1.3} {
		lc := int((p.X + dirX*m.TileSize*t) / m.TileSize)
		lr := int((p.Y + dirY*m.TileSize*t) / m.TileSize)
		if d, ok := m.DoorCells[[2]int{lc, lr}]; ok {
			connDoor = d
			break
		}
	}
	if connDoor != nil {
		distA := math.Hypot(p.X-connDoor.ExitA[0], p.Y-connDoor.ExitA[1])
		distB := math.Hypot(p.X-connDoor.ExitB[0], p.Y-connDoor.ExitB[1])
		if distA < distB {
			p.X, p.Y = connDoor.ExitB[0], connDoor.ExitB[1]
		} else {
			p.X, p.Y = connDoor.ExitA[0], connDoor.ExitA[1]
		}
	}

	// a picture straight ahead opens full-screen in the browser
	if connDoor == nil {
		if pic := lookedAtPicture(m, p.X, p.Y, dirX, dirY); pic != nil {
			if src := pictureViewURL(pic); src != "" {
				p.emit(Event{"type": "picture", "src": src})
			}
		}
	}

	e.scripts.fireUse(e, p, dirX, dirY)

	lookCol := int((p.X + dirX*m.TileSize*0.7) / m.TileSize)
	lookRow := int((p.Y + dirY*m.TileSize*0.7) / m.TileSize)
	if lookCol >= 0 && lookCol < m.Cols && lookRow >= 0 && lookRow < m.Rows {
		if target := e.levels[m.Grid[lookRow][lookCol].Portal]; target != nil {
			p.CurrentMap = target
			p.X, p.Y, p.Angle = portalArrival(target, m.Level)
		}
	}
}

// portalArrival is where a player coming from level `from` lands on m: in
// front of the portal leading back, facing away from it, or at m's spawn if
// there is no way back.
func portalArrival(m *Map, from string) (x, y, angle float64) {
	for r := 0; r < m.Rows; r++ {
		for c := 0; c < m.Cols; c++ {
			if m.Grid[r][c].Portal != from {
				continue
			}
			for _, d := range dirs {
				nc, nr := c+d[0], r+d[1]
				if walkableCell(m, nc, nr) {
					return (float64(nc) + 0.5) * m.TileSize, (float64(nr) + 0.5) * m.TileSize,
						math.Atan2(float64(d[1]), float64(d[0]))
				}
			}
		}
	}
	x, y = findSpawn(m)
	return x, y, 0
}

// afterMove runs everything keyed off the player's final position for the
// tick: onEnter scripts, music zones, global tick scripts.
func (e *Engine) afterMove(p *Player) {
	m := p.CurrentMap
	col, row := int(p.X/m.TileSize), int(p.Y/m.TileSize)
	if col != p.prevCol || row != p.prevRow {
		p.prevCol, p.prevRow = col, row
		if col >= 0 && col < m.Cols && row >= 0 && row < m.Rows {
			e.scripts.fireEnter(e, p, col, row)
		}
	}
	e.updateMusic(p)
	e.scripts.fireTicks(e)
}
