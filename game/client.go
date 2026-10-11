package game

import (
	"bytes"
	"encoding/json"
	"image"
	"io"
	"io/fs"
	"slices"
	"time"
)

// Client is the browser-side engine (compiled to WebAssembly by cmd/client).
// It holds the map the server last described, renders frames locally and
// predicts the player's own movement with the same code the server uses.
// Networking, timing and input stay in JavaScript (static/game.html); this
// type is plain Go so it is tested natively.
//
// Files are downloaded lazily: the texture loader asks the client filesystem
// for what it needs, unknown-but-listed files are queued in Wanted, and the
// page fetches them and hands them back through AddFile.
type Client struct {
	version   string
	known     map[string]bool   // every file in the content manifest
	files     map[string][]byte // downloaded so far
	wanted    map[string]bool   // needed, not yet handed to the page
	requested map[string]bool   // handed to the page

	m        *Map
	textures map[string]*Texture // shared by every map of this content version
	sprite   *Texture
	avatars  map[int]*Texture
	dirty    bool // map or files changed: re-resolve textures before drawing

	img  *image.NRGBA
	self Player
}

func NewClient() *Client {
	c := &Client{avatars: map[int]*Texture{}, img: image.NewNRGBA(image.Rect(0, 0, ScreenW, ScreenH))}
	c.resetContent("", nil)
	return c
}

func (c *Client) resetContent(version string, files []string) {
	c.version = version
	c.known = map[string]bool{}
	for _, f := range files {
		c.known[f] = true
	}
	c.files = map[string][]byte{}
	c.wanted = map[string]bool{}
	c.requested = map[string]bool{}
	c.textures = map[string]*Texture{}
	c.sprite = nil
	c.m = nil
}

// Apply consumes the content/map/cells parts of a ServerMsg. Pose, players
// and events are the page's business.
func (c *Client) Apply(data []byte) error {
	var msg ServerMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	if msg.Content != nil && msg.Content.Version != c.version {
		c.resetContent(msg.Content.Version, msg.Content.Files)
	}
	if s := msg.Map; s != nil {
		m := &Map{
			ID: s.ID, Cols: s.Cols, Rows: s.Rows, TileSize: TileSize,
			Grid:        make([][]Symbol, s.Rows),
			Textures:    c.textures,
			DoorCells:   map[[2]int]*Door{},
			PortalDoors: map[[2]int]*PortalDoor{},
		}
		for r := range m.Grid {
			m.Grid[r] = make([]Symbol, s.Cols)
			for col := range m.Grid[r] {
				if i := s.Grid[r*s.Cols+col]; i >= 0 && i < len(s.Palette) {
					m.Grid[r][col] = s.Palette[i]
				}
			}
		}
		for _, d := range s.Doors {
			m.DoorCells[d] = &Door{Col: d[0], Row: d[1]}
		}
		for _, mon := range s.Monsters {
			m.Monsters = append(m.Monsters, Monster{X: mon[0], Y: mon[1]})
		}
		c.m = m
		c.dirty = true
	}
	if m := c.m; m != nil {
		for _, cell := range msg.Cells {
			if cell.Col < 0 || cell.Col >= m.Cols || cell.Row < 0 || cell.Row >= m.Rows {
				continue
			}
			m.Grid[cell.Row][cell.Col] = cell.Sym
			k := [2]int{cell.Col, cell.Row}
			if cell.Door {
				m.DoorCells[k] = &Door{Col: cell.Col, Row: cell.Row}
			} else {
				delete(m.DoorCells, k)
			}
			c.dirty = true
		}
	}
	c.resolve()
	return nil
}

// Version is the content version the client currently holds.
func (c *Client) Version() string { return c.version }

// Wanted returns content paths the page should fetch, each once.
func (c *Client) Wanted() []string {
	out := make([]string, 0, len(c.wanted))
	for p := range c.wanted {
		out = append(out, p)
		c.requested[p] = true
	}
	clear(c.wanted)
	slices.Sort(out)
	return out
}

// AddFile hands over a downloaded content file.
func (c *Client) AddFile(path string, data []byte) {
	if !c.known[path] {
		return // stale download from a previous content version
	}
	c.files[path] = data
	c.dirty = true
	c.resolve()
}

// SetAvatar decodes another player's avatar image.
func (c *Client) SetAvatar(id int, data []byte) {
	if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
		c.avatars[id] = textureFromImage(img, true)
	}
}

// resolve loads whatever textures the current map needs and the files allow.
func (c *Client) resolve() {
	if !c.dirty || c.m == nil {
		return
	}
	c.dirty = false
	fsys := clientFS{c}
	for r := range c.m.Grid {
		for _, sym := range c.m.Grid[r] {
			ensureSymbolTextures(fsys, c.m, sym)
		}
	}
	if c.sprite == nil {
		if img, err := loadImageFile(fsys, spriteFile); err == nil {
			c.sprite = textureFromImage(img, true)
		}
	}
}

// Step predicts one 100 ms tick of movement for the held keys (mouse yaw is
// applied by the page directly).
func (c *Client) Step(x, y, angle float64, keys map[string]bool) (float64, float64, float64) {
	if c.m == nil {
		return x, y, angle
	}
	angle = turnAngle(angle, keys, 0)
	mx, my := moveVector(angle, keys)
	x, y = collide(c.m, x, y, mx, my)
	return x, y, angle
}

// Render draws the view from (x, y, angle). others is flat [id, x, y, ...].
// The returned RGBA buffer is reused by the next call.
func (c *Client) Render(x, y, angle float64, showMap bool, others []float64) []byte {
	if c.m == nil {
		return nil
	}
	c.resolve()
	c.self = Player{CurrentMap: c.m, X: x, Y: y, Angle: angle, ShowMap: showMap}
	var ps []*Player
	for i := 0; i+2 < len(others); i += 3 {
		ps = append(ps, &Player{
			CurrentMap: c.m, X: others[i+1], Y: others[i+2],
			Avatar: c.avatars[int(others[i])],
		})
	}
	newRenderer(c.sprite).renderInto(c.img, &c.self, ps)
	return c.img.Pix
}

// clientFS serves downloaded files; listed-but-missing ones are queued in
// wanted and reported as errPending.
type clientFS struct{ c *Client }

func (f clientFS) Open(name string) (fs.File, error) {
	if data, ok := f.c.files[name]; ok {
		return &memFile{name: name, Reader: bytes.NewReader(data)}, nil
	}
	if f.c.known[name] {
		if !f.c.requested[name] {
			f.c.wanted[name] = true
		}
		return nil, errPending
	}
	return nil, fs.ErrNotExist
}

type memFile struct {
	name string
	*bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return memInfo{f}, nil }
func (f *memFile) Close() error               { return nil }

type memInfo struct{ f *memFile }

func (i memInfo) Name() string       { return i.f.name }
func (i memInfo) Size() int64        { return i.f.Size() }
func (i memInfo) Mode() fs.FileMode  { return 0o444 }
func (i memInfo) ModTime() time.Time { return time.Time{} }
func (i memInfo) IsDir() bool        { return false }
func (i memInfo) Sys() any           { return nil }

var _ io.ReaderAt = (*memFile)(nil)
