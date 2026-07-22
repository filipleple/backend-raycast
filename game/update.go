package game

import "math"

// step applies one tick of input to p. Caller must hold the write lock.
// turnDelta is accumulated mouse movementX for this tick (screen px).
func (e *Engine) step(p *Player, inputs map[string]bool, turnDelta float64) {
	e.scripts.maybeReload()
	m := p.CurrentMap

	if inputs["ArrowLeft"] {
		p.Angle -= TurnSpeed
	}
	if inputs["ArrowRight"] {
		p.Angle += TurnSpeed
	}
	p.Angle += turnDelta * MouseSensitivity

	dirX, dirY := math.Cos(p.Angle), math.Sin(p.Angle)
	rightX, rightY := -dirY, dirX

	moveX, moveY := 0.0, 0.0

	if inputs["m"] && !p.prevInputs["m"] {
		p.ShowMap = !p.ShowMap
	}

	// --- door / portal interaction ---
	if inputs[" "] && !p.prevInputs[" "] {
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

		e.scripts.fireUse(e, p, dirX, dirY)

		lookCol := int((p.X + dirX*m.TileSize*0.7) / m.TileSize)
		lookRow := int((p.Y + dirY*m.TileSize*0.7) / m.TileSize)
		if portal, ok := m.PortalDoors[[2]int{lookCol, lookRow}]; ok {
			if portal.TargetMap == nil {
				if newMap, err := e.buildMap(); err == nil {
					px, py := findSpawn(newMap)
					portal.TargetMap = newMap
					portal.TargetPos = [2]float64{px, py}
					e.world.Maps = append(e.world.Maps, newMap)
				}
			}
			if portal.TargetMap != nil {
				p.CurrentMap = portal.TargetMap
				p.X, p.Y = portal.TargetPos[0], portal.TargetPos[1]
			}
		}
	}

	if inputs["ArrowUp"] {
		moveX += dirX * PlayerSpeed
		moveY += dirY * PlayerSpeed
	}
	if inputs["ArrowDown"] {
		moveX -= dirX * PlayerSpeed
		moveY -= dirY * PlayerSpeed
	}
	if inputs["a"] {
		moveX -= rightX * PlayerSpeed
		moveY -= rightY * PlayerSpeed
	}
	if inputs["d"] {
		moveX += rightX * PlayerSpeed
		moveY += rightY * PlayerSpeed
	}

	if mag := math.Hypot(moveX, moveY); mag > 0 {
		moveX = moveX / mag * PlayerSpeed
		moveY = moveY / mag * PlayerSpeed
	}

	// collision uses CurrentMap, which may have just changed via portal
	m = p.CurrentMap
	newX := p.X + moveX
	newY := p.Y + moveY
	ts := m.TileSize

	walkable := func(cx, cy int) bool {
		return cx >= 0 && cx < m.Cols && cy >= 0 && cy < m.Rows && m.Grid[cy][cx].Walkable
	}

	if moveX != 0 {
		cx := int((newX + math.Copysign(PlayerMargin, moveX)) / ts)
		cy := int(p.Y / ts)
		if !walkable(cx, cy) {
			newX = p.X
		}
	}
	if moveY != 0 {
		cx := int(newX / ts)
		cy := int((newY + math.Copysign(PlayerMargin, moveY)) / ts)
		if !walkable(cx, cy) {
			newY = p.Y
		}
	}

	p.X = newX
	p.Y = newY
	p.prevInputs = inputs

	// --- scripts + music (position is final for this tick) ---
	m = p.CurrentMap
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
