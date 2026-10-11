//go:build !js

package game

import (
	"encoding/json"
	"math"
	"sort"
	"time"
)

// Server side of the browser engine protocol (wire.go). The browser predicts
// its own movement and renders its own frames; the server validates moves,
// runs Space/doors/portals/scripts/music exactly as for JPEG sessions, and
// tells the browser what changed.

const (
	maxCellLog = 4096 // per-map change log; clients further behind get a snapshot

	// moveSlack forgives jitter: moves arrive bunched and late, so allow a
	// bit more than the exact distance PlayerSpeed covers in the elapsed time.
	moveSlack = 1.5
)

// ClientInput is what a client-engine session collected since its last tick.
type ClientInput struct {
	Move *ClientMsg // latest reported pose, nil if none arrived
	Use  bool       // Space was pressed
}

// ClientTick is one tick for a client-engine session: apply the reported
// move if it's plausible, run Space and the per-position game logic, and
// return the JSON message for the browser.
func (e *Engine) ClientTick(p *Player, in ClientInput) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scripts.maybeReload()

	ns := p.net
	if ns == nil {
		ns = &netState{lastMove: time.Now()}
		p.net = ns
	}

	// A move only counts if the client has seen the latest override and
	// nothing else (a script, another player's script, a reload) moved the
	// player since — otherwise the stale prediction would undo it.
	if mv := in.Move; mv != nil && ns.synced && mv.Epoch == ns.epoch && ns.matches(p) {
		now := time.Now()
		p.Angle = mv.Angle
		p.ShowMap = mv.ShowMap
		if plausibleMove(p.CurrentMap, p.X, p.Y, mv.X, mv.Y, now.Sub(ns.lastMove)) {
			p.X, p.Y = mv.X, mv.Y
		} else {
			ns.force = true // snap the client back to where the server has it
		}
		ns.lastMove = now
		ns.record(p)
	}

	if in.Use {
		e.use(p, math.Cos(p.Angle), math.Sin(p.Angle))
	}
	e.afterMove(p)
	e.syncGrids()

	return json.Marshal(e.clientMsg(p, ns))
}

// plausibleMove reports whether a client could have walked from (x0,y0) to
// (x1,y1) in elapsed: not too far, landing on a walkable cell, without
// passing through a wall. Per-axis collision moves X first, so an L-shaped
// path is accepted as well as the straight one.
func plausibleMove(m *Map, x0, y0, x1, y1 float64, elapsed time.Duration) bool {
	if x0 == x1 && y0 == y1 {
		return true
	}
	elapsed = min(max(elapsed, 0), time.Second)
	ticks := elapsed.Seconds()/0.1 + 1
	if math.Hypot(x1-x0, y1-y0) > PlayerSpeed*ticks*moveSlack {
		return false
	}
	clear := func(ax, ay, bx, by float64) bool {
		steps := int(math.Hypot(bx-ax, by-ay)/4) + 1
		for i := 0; i <= steps; i++ {
			f := float64(i) / float64(steps)
			x, y := ax+(bx-ax)*f, ay+(by-ay)*f
			if !walkableCell(m, int(x/m.TileSize), int(y/m.TileSize)) {
				return false
			}
		}
		return true
	}
	return clear(x0, y0, x1, y1) ||
		clear(x0, y0, x1, y0) && clear(x1, y0, x1, y1) ||
		clear(x0, y0, x0, y1) && clear(x0, y1, x1, y1)
}

func (ns *netState) matches(p *Player) bool {
	return ns.poseMap == p.CurrentMap && ns.x == p.X && ns.y == p.Y && ns.a == p.Angle && ns.showMap == p.ShowMap
}

func (ns *netState) record(p *Player) {
	ns.poseMap, ns.x, ns.y, ns.a, ns.showMap = p.CurrentMap, p.X, p.Y, p.Angle, p.ShowMap
}

// clientMsg assembles everything p's browser is missing. Caller holds the
// write lock.
func (e *Engine) clientMsg(p *Player, ns *netState) ServerMsg {
	msg := ServerMsg{Players: []PlayerMsg{}}
	if ns.content != e.contentVersion {
		msg.Content = &ContentMsg{Version: e.contentVersion, Files: e.contentFiles, Self: p.ID}
		ns.content = e.contentVersion
		ns.mapSent = nil // new assets: resend the grid too
	}

	m := p.CurrentMap
	if ns.mapSent != m || ns.rev < m.logFloor() {
		msg.Map = m.snapshot()
		ns.mapSent, ns.rev = m, m.rev
	} else if ns.rev < m.rev {
		msg.Cells = m.cellsSince(ns.rev)
		ns.rev = m.rev
	}

	if !ns.synced || ns.force || !ns.matches(p) {
		ns.epoch++
		ns.synced, ns.force = true, false
		ns.record(p)
		msg.Pose = &PoseMsg{X: p.X, Y: p.Y, Angle: p.Angle, ShowMap: p.ShowMap, Epoch: ns.epoch}
	}

	for _, q := range e.players {
		if q != p && q.CurrentMap == m {
			msg.Players = append(msg.Players, PlayerMsg{ID: q.ID, X: q.X, Y: q.Y, Avatar: len(q.avatarRaw) > 0})
		}
	}
	msg.Events = p.events
	p.events = nil
	return msg
}

// syncGrids diffs every map against what was last broadcast and logs the
// changed cells. Diffing (rather than hooking setCellLayer) also catches
// scripts mutating ctx.cell() symbols directly.
func (e *Engine) syncGrids() {
	for _, m := range e.world.Maps {
		m.syncShadow()
	}
}

func (m *Map) cellState(c, r int) cellState {
	_, door := m.DoorCells[[2]int{c, r}]
	return cellState{sym: m.Grid[r][c], door: door}
}

func (m *Map) syncShadow() {
	if m.shadow == nil {
		m.shadow = make([]cellState, m.Rows*m.Cols)
		for r := 0; r < m.Rows; r++ {
			for c := 0; c < m.Cols; c++ {
				m.shadow[r*m.Cols+c] = m.cellState(c, r)
			}
		}
		return
	}
	bumped := false
	for r := 0; r < m.Rows; r++ {
		for c := 0; c < m.Cols; c++ {
			cur := m.cellState(c, r)
			if i := r*m.Cols + c; cur != m.shadow[i] {
				if !bumped {
					m.rev++
					bumped = true
				}
				m.shadow[i] = cur
				m.changes = append(m.changes, cellChange{rev: m.rev, col: c, row: r})
			}
		}
	}
	if len(m.changes) > maxCellLog {
		m.changes = append([]cellChange(nil), m.changes[len(m.changes)-maxCellLog/2:]...)
	}
}

// logFloor is the oldest revision the change log can bring a client up from.
func (m *Map) logFloor() int {
	if len(m.changes) == 0 {
		return m.rev
	}
	return m.changes[0].rev - 1
}

// cellsSince returns the current state of every cell changed after rev.
func (m *Map) cellsSince(rev int) []CellMsg {
	i := sort.Search(len(m.changes), func(i int) bool { return m.changes[i].rev > rev })
	seen := map[[2]int]bool{}
	var out []CellMsg
	for _, ch := range m.changes[i:] {
		k := [2]int{ch.col, ch.row}
		if seen[k] {
			continue
		}
		seen[k] = true
		s := m.shadow[ch.row*m.Cols+ch.col]
		out = append(out, CellMsg{Col: ch.col, Row: ch.row, Sym: s.sym, Door: s.door})
	}
	return out
}

// snapshot encodes the whole grid (as last synced) for the browser.
func (m *Map) snapshot() *MapSnapshot {
	m.syncShadow()
	s := &MapSnapshot{ID: m.ID, Cols: m.Cols, Rows: m.Rows, Grid: make([]int, len(m.shadow)),
		Doors: [][2]int{}, Monsters: [][2]float64{}}
	index := map[Symbol]int{}
	for i, cs := range m.shadow {
		j, ok := index[cs.sym]
		if !ok {
			j = len(s.Palette)
			index[cs.sym] = j
			s.Palette = append(s.Palette, cs.sym)
		}
		s.Grid[i] = j
		if cs.door {
			s.Doors = append(s.Doors, [2]int{i % m.Cols, i / m.Cols})
		}
	}
	for _, mon := range m.Monsters {
		s.Monsters = append(s.Monsters, [2]float64{mon.X, mon.Y})
	}
	return s
}
