package game

import "math"

// Movement shared by the server's step() and the browser engine's prediction,
// so both agree to the pixel on where a set of held keys takes a player.

// turnAngle applies one tick of arrow-key turning plus mouse yaw.
func turnAngle(angle float64, inputs map[string]bool, turnDelta float64) float64 {
	if inputs["ArrowLeft"] {
		angle -= TurnSpeed
	}
	if inputs["ArrowRight"] {
		angle += TurnSpeed
	}
	return angle + turnDelta*MouseSensitivity
}

// moveVector is one tick of walking/strafing for the held keys, normalized to
// PlayerSpeed.
func moveVector(angle float64, inputs map[string]bool) (moveX, moveY float64) {
	dirX, dirY := math.Cos(angle), math.Sin(angle)
	rightX, rightY := -dirY, dirX

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
	return moveX, moveY
}

// walkableCell reports whether (cx, cy) is inside m and passable.
func walkableCell(m *Map, cx, cy int) bool {
	return cx >= 0 && cx < m.Cols && cy >= 0 && cy < m.Rows && m.Grid[cy][cx].Walkable
}

// collide applies (moveX, moveY) from (x, y) with per-axis collision against
// PlayerMargin, X first.
func collide(m *Map, x, y, moveX, moveY float64) (float64, float64) {
	newX := x + moveX
	newY := y + moveY
	ts := m.TileSize

	if moveX != 0 {
		cx := int((newX + math.Copysign(PlayerMargin, moveX)) / ts)
		cy := int(y / ts)
		if !walkableCell(m, cx, cy) {
			newX = x
		}
	}
	if moveY != 0 {
		cx := int(newX / ts)
		cy := int((newY + math.Copysign(PlayerMargin, moveY)) / ts)
		if !walkableCell(m, cx, cy) {
			newY = y
		}
	}
	return newX, newY
}
