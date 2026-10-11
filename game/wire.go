package game

import "time"

// Wire format between the server and the browser-side engine. The server
// sends one ServerMsg per tick as a JSON text frame (netsync.go); the browser
// answers with ClientMsg moves and uses (client.go + static/game.html).

// ServerMsg is one tick's worth of state for a client-side engine. Fields are
// applied in declaration order: content, then map, then cells, then pose.
type ServerMsg struct {
	Content *ContentMsg  `json:"content,omitempty"` // asset set: on join and content reload
	Map     *MapSnapshot `json:"map,omitempty"`     // full grid: join, map change, reload, fell behind
	Cells   []CellMsg    `json:"cells,omitempty"`   // grid changes since the last message
	Pose    *PoseMsg     `json:"pose,omitempty"`    // authoritative override of the predicted pose
	Players []PlayerMsg  `json:"players"`           // everyone else on this map
	Events  []Event      `json:"ev,omitempty"`      // music, popups, sounds, picture, ...
}

// ContentMsg lists every file the browser may fetch from /content/<path>,
// versioned so URLs can be cached forever.
type ContentMsg struct {
	Version string   `json:"version"`
	Files   []string `json:"files"`
	Self    int      `json:"self"` // the receiving player's id
}

// MapSnapshot is a whole map grid, palette-compressed: maps are mostly a few
// dozen distinct cells repeated.
type MapSnapshot struct {
	ID       int          `json:"id"`
	Cols     int          `json:"cols"`
	Rows     int          `json:"rows"`
	Palette  []Symbol     `json:"palette"`
	Grid     []int        `json:"grid"`  // row-major palette indices
	Doors    [][2]int     `json:"doors"` // connectivity door cells, for the minimap
	Monsters [][2]float64 `json:"monsters"`
}

// CellMsg is one changed cell.
type CellMsg struct {
	Col  int    `json:"c"`
	Row  int    `json:"r"`
	Sym  Symbol `json:"s"`
	Door bool   `json:"d,omitempty"`
}

// PoseMsg overrides the browser's position: teleports, doors, portals,
// scripts writing ctx.player, rejected moves. Moves stamped with an older
// Epoch are ignored, so in-flight predictions can't undo an override.
type PoseMsg struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Angle   float64 `json:"a"`
	ShowMap bool    `json:"m"`
	Epoch   int     `json:"e"`
}

// PlayerMsg is another player on the same map.
type PlayerMsg struct {
	ID     int     `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Avatar bool    `json:"av,omitempty"` // fetch /avatar/player/<id>
}

// ClientMsg is sent by the browser: T "move" reports the predicted pose
// (~20 Hz), T "use" is a Space press.
type ClientMsg struct {
	T       string  `json:"t"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Angle   float64 `json:"a"`
	ShowMap bool    `json:"m"`
	Epoch   int     `json:"e"`
}

// cellState is what the browser knows about one cell.
type cellState struct {
	sym  Symbol
	door bool
}

// cellChange records that (col, row) changed in revision rev.
type cellChange struct {
	rev, col, row int
}

// netState is the server's view of what one client-side engine has.
type netState struct {
	content string // content version sent
	mapSent *Map   // map whose snapshot was sent
	rev     int    // mapSent revision the client is at

	epoch    int  // current pose epoch
	synced   bool // a pose has been sent at all
	poseMap  *Map // the pose the client is known to have
	x, y, a  float64
	showMap  bool
	force    bool // resend the pose even if it matches (rejected move)
	lastMove time.Time
}
