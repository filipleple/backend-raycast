package game

// Event is one JSON control message for the browser (music change, popup,
// sound, ...). Events ride the WebSocket as text frames; binary frames stay
// JPEG video. The client dispatches on the "type" key and ignores types it
// doesn't know, so scripts can invent new ones freely.
type Event map[string]any

// emit queues ev for delivery on p's next tick. Caller must hold the write
// lock (any player's handler may emit to any other player).
func (p *Player) emit(ev Event) {
	p.events = append(p.events, ev)
}

// DrainEvents returns and clears p's queued events.
func (e *Engine) DrainEvents(p *Player) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	evs := p.events
	p.events = nil
	return evs
}
