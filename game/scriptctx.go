package game

import "log"

// ScriptCtx is everything a script handler can touch. goja exposes it to JS
// with lowercased names: ctx.player, ctx.col, ctx.setWall(...), etc.
//
// It is intentionally NOT a safe sandbox: ctx.player's fields (x, y, angle,
// showMap, currentMap...) are writable, ctx.cell() hands out a live Symbol
// pointer, and ctx.js() runs arbitrary code in the player's browser. Scripts
// are repo code, same trust level as main.go — see scripts/README.md.
type ScriptCtx struct {
	Player *Player // triggering player; nil inside onTick handlers
	Col    int     // triggering cell; -1 when not cell-bound
	Row    int
	e      *Engine
}

// ---- messages to the browser -------------------------------------------

// Popup shows an on-screen message for ms milliseconds (0 → 3000).
func (c *ScriptCtx) Popup(text string, ms int) { c.SendTo(c.Player, popupEvent(text, ms)) }

// PopupAll shows the popup to every connected player.
func (c *ScriptCtx) PopupAll(text string, ms int) {
	for _, p := range c.e.players {
		c.SendTo(p, popupEvent(text, ms))
	}
}

func popupEvent(text string, ms int) Event {
	if ms <= 0 {
		ms = 3000
	}
	return Event{"type": "popup", "text": text, "ms": ms}
}

// Tone beeps the triggering player's browser: a synthesized note, no asset
// needed. vol 0 → 0.5; delayMs staggers notes into little melodies.
func (c *ScriptCtx) Tone(freq float64, ms int, vol float64, delayMs int) {
	c.SendTo(c.Player, toneEvent(freq, ms, vol, delayMs))
}

// ToneAll beeps everyone.
func (c *ScriptCtx) ToneAll(freq float64, ms int, vol float64, delayMs int) {
	for _, p := range c.e.players {
		c.SendTo(p, toneEvent(freq, ms, vol, delayMs))
	}
}

func toneEvent(freq float64, ms int, vol float64, delayMs int) Event {
	if ms <= 0 {
		ms = 200
	}
	if vol <= 0 {
		vol = 0.5
	}
	return Event{"type": "tone", "freq": freq, "ms": ms, "vol": vol, "delay": delayMs}
}

// Sound plays a one-shot audio file (URL or bare name resolved to
// /ost/<name>) in the triggering player's browser. vol 0 → 1.
func (c *ScriptCtx) Sound(file string, vol float64) { c.SendTo(c.Player, soundEvent(file, vol)) }

// SoundAll plays it for everyone.
func (c *ScriptCtx) SoundAll(file string, vol float64) {
	for _, p := range c.e.players {
		c.SendTo(p, soundEvent(file, vol))
	}
}

func soundEvent(file string, vol float64) Event {
	if vol <= 0 {
		vol = 1
	}
	if file != "" && file[0] != '/' {
		file = "/ost/" + file
	}
	return Event{"type": "sound", "file": file, "vol": vol}
}

// Music pins the triggering player's soundtrack to a zone ID from
// MUSIC_DEFS.csv (or any M#### with a matching /ost/ file), overriding
// MUSIC.csv until musicAuto() is called. "M0000" is silence.
func (c *ScriptCtx) Music(id string) {
	if c.Player == nil {
		return
	}
	c.Player.musicLocked = true
	c.e.setMusic(c.Player, id)
}

// MusicAuto hands control back to the MUSIC.csv zones.
func (c *ScriptCtx) MusicAuto() {
	if c.Player == nil {
		return
	}
	c.Player.musicLocked = false
	c.Player.musicZone = "" // force a re-emit from the current zone
}

// Js runs arbitrary JavaScript in the triggering player's browser. Yes,
// really. The client evals it with no questions asked.
func (c *ScriptCtx) Js(code string) { c.SendTo(c.Player, Event{"type": "eval", "js": code}) }

// JsAll runs it in every connected browser.
func (c *ScriptCtx) JsAll(code string) {
	for _, p := range c.e.players {
		c.SendTo(p, Event{"type": "eval", "js": code})
	}
}

// Send queues an arbitrary event for the triggering player — invent your own
// type and handle it in game.html's HANDLERS table.
func (c *ScriptCtx) Send(ev map[string]any) { c.SendTo(c.Player, Event(ev)) }

// SendTo queues an arbitrary event for a specific player.
func (c *ScriptCtx) SendTo(p *Player, ev Event) {
	if p != nil {
		p.emit(ev)
	}
}

// ---- world mutation ------------------------------------------------------

// SetWall rewrites a cell's wall layer to a tile ID from definitions.csv.
// Global: every player sees it. setWall(col, row, "0000") opens a hole.
func (c *ScriptCtx) SetWall(col, row int, id string) { c.e.setCellLayer(c.mapOf(), col, row, 1, id) }

// SetFloor rewrites a cell's floor layer.
func (c *ScriptCtx) SetFloor(col, row int, id string) { c.e.setCellLayer(c.mapOf(), col, row, 2, id) }

// SetCeiling rewrites a cell's ceiling layer.
func (c *ScriptCtx) SetCeiling(col, row int, id string) {
	c.e.setCellLayer(c.mapOf(), col, row, 0, id)
}

// Cell returns the live Symbol at (col, row) — mutate its fields directly to
// bypass definitions entirely (invisible walls, walk-through textures, ...).
// Returns nil out of bounds.
func (c *ScriptCtx) Cell(col, row int) *Symbol {
	m := c.mapOf()
	if col < 0 || col >= m.Cols || row < 0 || row >= m.Rows {
		return nil
	}
	return &m.Grid[row][col]
}

// Teleport drops a player at the center of (col, row). Fractional
// coordinates work. No walkability check — you can absolutely put someone
// inside a wall.
func (c *ScriptCtx) Teleport(p *Player, col, row float64) {
	if p == nil {
		return
	}
	ts := p.CurrentMap.TileSize
	p.X, p.Y = (col+0.5)*ts, (row+0.5)*ts
}

// ---- world queries -------------------------------------------------------

// Players returns every connected player (live pointers, all writable).
func (c *ScriptCtx) Players() []*Player { return c.e.players }

// Map returns the triggering player's current map (or the first map in
// onTick handlers).
func (c *ScriptCtx) Map() *Map { return c.mapOf() }

// Log prints to the server log with a [script] prefix.
func (c *ScriptCtx) Log(args ...any) { log.Println(append([]any{"[script]"}, args...)...) }

func (c *ScriptCtx) mapOf() *Map {
	if c.Player != nil {
		return c.Player.CurrentMap
	}
	return c.e.world.Maps[0]
}
