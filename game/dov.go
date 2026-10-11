//go:build !js

package game

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// Department of Vision gallery: 629fm's /api/dov/images on the walls. Picture
// cells whose texture is "dov" are gallery slots. RunDoV polls the gallery
// list and every rotation shows the next len(slots) images: each is
// downloaded, laid into its slot's frame and encoded as a 256-px PNG. The
// server decodes that PNG for itself and browsers download the same bytes from
// /content/, so both render identical pixels. Rebinding a slot is a plain cell
// change, which reaches players already in the room as a grid delta.

const (
	dovMaxBytes     = 13 << 20 // 629fm caps image uploads at 12 MB
	dovMaxSide      = 8192
	dovMaxPixels    = 16 << 20
	dovRetryFailed  = 10 * time.Minute
	dovFetchWorkers = 4
)

// dovFormats are the gallery mimes Go can decode; webp, avif and video stay
// off the walls.
var dovFormats = []string{"image/png", "image/jpeg", "image/gif"}

// DovConfig locates the 629fm gallery.
type DovConfig struct {
	APIURL string        // base for /api/dov/... (https://629fm.com, http://api:3000)
	WebURL string        // base for /archive/... ("" = APIURL)
	Poll   time.Duration // gallery list refresh
	Rotate time.Duration // how long one set of images stays up
}

// sourceURL resolves a gallery src. Only the two known path shapes are
// fetched, so the list can't point the server anywhere else.
func (c DovConfig) sourceURL(src string) string {
	if strings.Contains(src, "..") {
		return ""
	}
	switch {
	case strings.HasPrefix(src, "/api/dov/file/"):
		return strings.TrimRight(c.APIURL, "/") + src
	case strings.HasPrefix(src, "/archive/photos/dov/"):
		web := c.WebURL
		if web == "" {
			web = c.APIURL
		}
		return strings.TrimRight(web, "/") + src
	}
	return ""
}

type dovItem struct {
	ID   int    `json:"id"`
	Mime string `json:"mime"`
	Src  string `json:"src"`
}

// dovEntry is one composed slot image.
type dovEntry struct {
	png    []byte
	tex    *Texture
	source string // the original, for the full-screen view
	mime   string
}

type dovState struct {
	root string

	// mu guards entries, frames and gen. It is taken inside Engine.mu (by
	// bindLocked) but never the other way round.
	mu      sync.RWMutex
	entries map[string]*dovEntry // composite name -> entry
	frames  map[string]*Texture  // frame name -> cropped 256-px frame (nil = missing)
	gen     int                  // bumped on content reload: frames may have changed
	frameMu sync.Mutex           // one frame resample at a time (golden.png is 13 MP)

	binding []string // composite name per slot (dovSlots order), "" = vacant; Engine.mu
}

func newDovState(root string) *dovState {
	return &dovState{root: root, entries: map[string]*dovEntry{}, frames: map[string]*Texture{}}
}

// dovSlot is one gallery slot cell.
type dovSlot struct {
	m        *Map
	col, row int
	frame    string
}

// dovSlots lists every slot on every map, each map's in walking order from its
// spawn so consecutive gallery images hang next to each other. Caller holds
// e.mu.
func (e *Engine) dovSlots() []dovSlot {
	var out []dovSlot
	for _, m := range e.world.Maps {
		var slots []dovSlot
		for r := range m.Grid {
			for c, sym := range m.Grid[r] {
				if d := defOr(e.defs, sym.WallID); d.Picture && d.TextureName == dovSlotTexture {
					slots = append(slots, dovSlot{m: m, col: c, row: r, frame: pictureFrameName(d)})
				}
			}
		}
		if len(slots) == 0 {
			continue
		}
		dist := walkDistances(m)
		reach := func(s dovSlot) int {
			best := len(dist) // unreachable slots go last
			for _, d := range dirs {
				if c, r := s.col+d[0], s.row+d[1]; c >= 0 && c < m.Cols && r >= 0 && r < m.Rows {
					if v := dist[r*m.Cols+c]; v >= 0 && v < best {
						best = v
					}
				}
			}
			return best
		}
		slices.SortStableFunc(slots, func(a, b dovSlot) int {
			if d := reach(a) - reach(b); d != 0 {
				return d
			}
			if a.row != b.row {
				return a.row - b.row
			}
			return a.col - b.col
		})
		out = append(out, slots...)
	}
	return out
}

// walkDistances is the BFS step count from m's spawn to every cell, -1 where
// unreachable.
func walkDistances(m *Map) []int {
	dist := make([]int, m.Rows*m.Cols)
	for i := range dist {
		dist[i] = -1
	}
	sx, sy := findSpawn(m)
	start := [2]int{int(sx / m.TileSize), int(sy / m.TileSize)}
	dist[start[1]*m.Cols+start[0]] = 0
	queue := [][2]int{start}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, d := range dirs {
			c, r := p[0]+d[0], p[1]+d[1]
			if walkableCell(m, c, r) && dist[r*m.Cols+c] < 0 {
				dist[r*m.Cols+c] = dist[p[1]*m.Cols+p[0]] + 1
				queue = append(queue, [2]int{c, r})
			}
		}
	}
	return dist
}

// bindLocked writes the current binding into the slot cells of every map and
// drops gallery textures nothing shows any more. Caller holds e.mu.
func (d *dovState) bindLocked(e *Engine) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	fsys := os.DirFS(e.root)
	inUse := map[string]bool{}
	for i, s := range e.dovSlots() {
		old := s.m.Grid[s.row][s.col]
		sym := makeSymbol(e.defs, old.CeilingID, old.WallID, old.FloorID)
		if i < len(d.binding) {
			if ent := d.entries[d.binding[i]]; ent != nil {
				sym.TextureName, sym.FrameTexture = dovTexturePrefix+d.binding[i], ""
				s.m.Textures[sym.wallTextureKey()] = ent.tex
				inUse[sym.wallTextureKey()] = true
			}
		}
		ensureSymbolTextures(fsys, s.m, sym)
		s.m.Grid[s.row][s.col] = sym
	}
	for _, m := range e.world.Maps {
		for key := range m.Textures {
			if strings.HasPrefix(key, "picture:"+dovTexturePrefix) && !inUse[key] {
				delete(m.Textures, key)
			}
		}
	}
}

// reloadedLocked re-applies the gallery to freshly loaded maps; frames are
// re-read for the next rotation. Caller holds e.mu.
func (d *dovState) reloadedLocked(e *Engine) {
	d.mu.Lock()
	clear(d.frames)
	d.gen++
	d.mu.Unlock()
	d.bindLocked(e)
}

// file is a composite's PNG, nil if unknown.
func (d *dovState) file(name string) []byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if ent := d.entries[name]; ent != nil {
		return ent.png
	}
	return nil
}

// RunDoV keeps the gallery slots filled until ctx ends.
func (e *Engine) RunDoV(ctx context.Context, cfg DovConfig) {
	d := e.dov
	client := &http.Client{Timeout: 15 * time.Second}
	failed := map[string]time.Time{} // src -> when it last failed
	var pool []dovItem
	var lastPoll time.Time
	listDown := false
	offset := 0

	for {
		if pool == nil || time.Since(lastPoll) >= cfg.Poll {
			items, err := fetchDovList(ctx, client, cfg)
			switch {
			case err != nil:
				if !listDown {
					log.Printf("dov: gallery list: %v (keeping the current images)", err)
				}
				listDown = true
			default:
				if listDown || pool == nil {
					log.Printf("dov: gallery has %d wall-able images", len(items))
				}
				listDown = false
				pool, lastPoll = items, time.Now()
			}
		}

		var shown []dovItem
		for _, it := range pool {
			if t, bad := failed[it.Src]; !bad || time.Since(t) >= dovRetryFailed {
				shown = append(shown, it)
			}
		}

		e.mu.RLock()
		slots := e.dovSlots()
		e.mu.RUnlock()

		if len(shown) > 0 && len(slots) > 0 {
			offset %= len(shown)
			names := make([]string, min(len(slots), len(shown)))
			var wg sync.WaitGroup
			var failMu sync.Mutex
			sem := make(chan struct{}, dovFetchWorkers)
			for i := range names {
				it := shown[(offset+i)%len(shown)]
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					name, err := d.ensure(ctx, client, cfg, it, slots[i].frame)
					failMu.Lock()
					defer failMu.Unlock()
					if err != nil {
						log.Printf("dov: %s: %v", it.Src, err)
						failed[it.Src] = time.Now()
						return
					}
					delete(failed, it.Src)
					names[i] = name
				}()
			}
			wg.Wait()

			e.mu.Lock()
			prev := d.binding
			d.binding = names
			d.bindLocked(e)
			e.mu.Unlock()
			d.prune(names, prev)

			if len(shown) > len(slots) {
				offset += len(slots)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.Rotate):
		}
	}
}

// prune forgets composites outside the current and previous binding (a
// browser may still be downloading the previous set).
func (d *dovState) prune(cur, prev []string) {
	keep := map[string]bool{}
	for _, n := range append(slices.Clone(cur), prev...) {
		keep[n] = true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for n := range d.entries {
		if !keep[n] {
			delete(d.entries, n)
		}
	}
}

func fetchDovList(ctx context.Context, client *http.Client, cfg DovConfig) ([]dovItem, error) {
	body, err := dovGet(ctx, client, strings.TrimRight(cfg.APIURL, "/")+"/api/dov/images", 4<<20)
	if err != nil {
		return nil, err
	}
	var resp struct {
		OK     bool      `json:"ok"`
		Images []dovItem `json:"images"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, errors.New("not ok")
	}
	items := []dovItem{}
	for _, it := range resp.Images {
		if slices.Contains(dovFormats, it.Mime) && cfg.sourceURL(it.Src) != "" {
			items = append(items, it)
		}
	}
	return items, nil
}

func dovGet(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("too large")
	}
	return body, nil
}

// dovName names the composite of src in frame. It changes with the content
// generation, so a reloaded frame never hides behind a cached URL.
func dovName(src, frame string, gen int) string {
	h := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s\x00%d", src, frame, gen))
	return hex.EncodeToString(h[:10])
}

// ensure composes it in frame unless that composite already exists, and
// returns its name.
func (d *dovState) ensure(ctx context.Context, client *http.Client, cfg DovConfig, it dovItem, frame string) (string, error) {
	d.mu.RLock()
	name := dovName(it.Src, frame, d.gen)
	_, have := d.entries[name]
	d.mu.RUnlock()
	if have {
		return name, nil
	}

	source := cfg.sourceURL(it.Src)
	data, err := dovGet(ctx, client, source, dovMaxBytes)
	if err != nil {
		return "", err
	}
	pic, err := decodeDovImage(data)
	if err != nil {
		return "", err
	}
	var out *Texture
	if f := d.frame(frame); f != nil {
		out = composeFramed(pic, f)
	} else {
		out = resampleArea(pic, image.Rect(0, 0, pic.W, pic.H), pictureTexSize, pictureTexSize)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, &image.NRGBA{Pix: out.Pix, Stride: out.W * 4, Rect: image.Rect(0, 0, out.W, out.H)}); err != nil {
		return "", err
	}
	// decode our own PNG exactly like a browser engine's texture loader will
	img, err := png.Decode(bytes.NewReader(buf.Bytes()))
	if err != nil {
		return "", err
	}

	d.mu.Lock()
	d.entries[name] = &dovEntry{png: buf.Bytes(), tex: textureFromImage(img, false), source: source, mime: it.Mime}
	d.mu.Unlock()
	return name, nil
}

func decodeDovImage(data []byte) (*Texture, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > dovMaxSide || cfg.Height > dovMaxSide || cfg.Width*cfg.Height > dovMaxPixels {
		return nil, fmt.Errorf("image is %dx%d, too large", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return textureFromImage(img, false), nil
}

// frame is name cropped and scaled to the composite size, nil if missing.
func (d *dovState) frame(name string) *Texture {
	d.frameMu.Lock()
	defer d.frameMu.Unlock()
	d.mu.RLock()
	f, ok := d.frames[name]
	d.mu.RUnlock()
	if ok {
		return f
	}
	if t := loadTextureFile(os.DirFS(d.root), name, "frames", true); t != nil {
		f = resampleArea(t, opaqueBounds(t, frameAlphaCut), pictureTexSize, pictureTexSize)
	}
	d.mu.Lock()
	d.frames[name] = f
	d.mu.Unlock()
	return f
}

// DovViewHandler serves the original image behind a filled slot, for the
// full-screen viewer: <prefix><composite name>.
func (e *Engine) DovViewHandler(prefix string) http.Handler {
	client := &http.Client{Timeout: 30 * time.Second}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, prefix)
		d := e.dov
		d.mu.RLock()
		ent := d.entries[name]
		d.mu.RUnlock()
		if ent == nil {
			http.NotFound(w, r)
			return
		}
		data, err := dovGet(r.Context(), client, ent.source, dovMaxBytes)
		if err != nil {
			http.Error(w, "gallery unavailable", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", ent.mime)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(data)
	})
}
