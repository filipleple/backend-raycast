package game

// Screen and world constants. World scale is fixed, independent of screen and
// map size. Movement is in world pixels; speed + margin must stay below
// TileSize or the per-axis collision check can skip over a one-tile wall.
// spriteFile is the monster / avatar-fallback sprite, relative to content.
const spriteFile = "hatman.gif"

const (
	ScreenW = 320
	ScreenH = 240

	FOVDegrees = 60
	NumRays    = 120

	TileSize     = 64.0
	PlayerSpeed  = TileSize * 0.25 // world px per tick
	TurnSpeed    = 0.14            // radians per tick for arrow-key turning
	PlayerMargin = TileSize * 0.2  // collision radius in world px

	// MouseSensitivity converts accumulated mouse movementX (screen px) into
	// radians of yaw per tick. Tune to taste.
	MouseSensitivity = 0.003
)

type Monster struct {
	X, Y float64
}

// Map is a self-contained room.
type Map struct {
	Cols, Rows  int
	TileSize    float64
	Grid        [][]Symbol
	Textures    map[string]*Texture
	Monsters    []Monster
	DoorCells   map[[2]int]*Door
	PortalDoors map[[2]int]*PortalDoor
	MusicZones  [][]string // parallel MUSIC.csv grid; nil = no zone music

	// ID names the map on the wire (index into World.Maps). The rest is the
	// server's change log for client-side engines; see netsync.go.
	ID      int
	shadow  []cellState
	rev     int
	changes []cellChange
}

type World struct {
	Maps []*Map
}

type Player struct {
	ID         int
	Name       string
	Avatar     *Texture // nil falls back to hatman
	CurrentMap *Map
	X, Y       float64
	Angle      float64
	ShowMap    bool
	prevInputs map[string]bool

	events []Event // outgoing control messages, drained each tick

	// music-zone state machine
	musicZone    string
	musicLocked  bool // a script pinned the track; ignore zones
	pendingZone  string
	pendingTicks int

	// last cell, for onEnter edge detection
	prevCol, prevRow int

	avatarRaw []byte    // uploaded avatar as-is, served to client-side engines
	net       *netState // client-side engine sync state; nil for JPEG sessions
}

// Event is one JSON control message for the browser (music change, popup,
// sound, ...). Events ride the WebSocket as text frames; binary frames stay
// JPEG video. The client dispatches on the "type" key and ignores types it
// doesn't know, so scripts can invent new ones freely.
type Event map[string]any
