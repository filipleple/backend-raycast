package game

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/dop251/goja"
)

// scriptHost embeds a JavaScript runtime (goja) and runs every scripts/*.js
// at startup. Scripts register handlers (onEnter/onUse/onTick/onJoin) that
// the engine fires at the right moments; they never run on their own.
//
// Everything here executes under the engine write lock, so handlers may
// mutate the world freely. Handlers are deliberately given sharp tools
// (raw cell access, direct player fields, browser eval) — a script CAN break
// physics, strand a player in a wall or crash their session. That's a
// feature. Errors and panics are logged and never kill the server.
type scriptHost struct {
	dir string

	vm        *goja.Runtime
	enterTile map[string][]goja.Callable
	enterCell map[[2]int][]goja.Callable
	useTile   map[string][]goja.Callable
	useCell   map[[2]int][]goja.Callable
	joins     []goja.Callable
	ticks     []*tickHandler

	mtimes    map[string]time.Time
	lastCheck time.Time
}

type tickHandler struct {
	every time.Duration
	last  time.Time
	fn    goja.Callable
}

func newScriptHost(dir string) *scriptHost {
	h := &scriptHost{dir: dir, lastCheck: time.Now()}
	h.reload()
	return h
}

func (h *scriptHost) scanMtimes() map[string]time.Time {
	out := map[string]time.Time{}
	paths, _ := filepath.Glob(filepath.Join(h.dir, "*.js"))
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out[p] = fi.ModTime()
		}
	}
	return out
}

// reload builds a fresh runtime and re-runs every script. A file that fails
// to parse or throws at load time is logged and skipped; the rest still load.
func (h *scriptHost) reload() {
	h.vm = goja.New()
	h.vm.SetFieldNameMapper(goja.UncapFieldNameMapper())
	h.enterTile = map[string][]goja.Callable{}
	h.enterCell = map[[2]int][]goja.Callable{}
	h.useTile = map[string][]goja.Callable{}
	h.useCell = map[[2]int][]goja.Callable{}
	h.joins = nil
	h.ticks = nil

	register := func(tile map[string][]goja.Callable, cell map[[2]int][]goja.Callable) func(goja.Value, goja.Callable) {
		return func(spec goja.Value, fn goja.Callable) {
			if pos, ok := cellSpec(spec); ok {
				cell[pos] = append(cell[pos], fn)
			} else {
				tile[spec.String()] = append(tile[spec.String()], fn)
			}
		}
	}
	h.vm.Set("onEnter", register(h.enterTile, h.enterCell))
	h.vm.Set("onUse", register(h.useTile, h.useCell))
	h.vm.Set("onJoin", func(fn goja.Callable) { h.joins = append(h.joins, fn) })
	h.vm.Set("onTick", func(every int, fn goja.Callable) {
		if every < 1 {
			every = 1
		}
		h.ticks = append(h.ticks, &tickHandler{every: time.Duration(every) * 100 * time.Millisecond, fn: fn})
	})
	h.vm.Set("log", func(args ...any) { log.Println(append([]any{"[script]"}, args...)...) })

	h.mtimes = h.scanMtimes()
	paths, _ := filepath.Glob(filepath.Join(h.dir, "*.js"))
	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			log.Printf("[script] %s: %v", p, err)
			continue
		}
		if _, err := h.vm.RunScript(filepath.Base(p), string(src)); err != nil {
			log.Printf("[script] %s failed to load: %v", p, err)
			continue
		}
		log.Printf("[script] loaded %s", filepath.Base(p))
	}
}

// maybeReload hot-reloads the scripts folder when any *.js changed. Checked
// at most once per second; runs under the engine write lock via step().
func (h *scriptHost) maybeReload() {
	if time.Since(h.lastCheck) < time.Second {
		return
	}
	h.lastCheck = time.Now()
	cur := h.scanMtimes()
	if len(cur) != len(h.mtimes) {
		log.Println("[script] change detected, reloading")
		h.reload()
		return
	}
	for p, t := range cur {
		if old, ok := h.mtimes[p]; !ok || !old.Equal(t) {
			log.Println("[script] change detected, reloading")
			h.reload()
			return
		}
	}
}

// cellSpec accepts [col,row] arrays or {col,row} objects; anything else is
// treated as a tile ID string by the caller.
func cellSpec(v goja.Value) ([2]int, bool) {
	obj, ok := v.(*goja.Object)
	if !ok {
		return [2]int{}, false
	}
	if c := obj.Get("col"); c != nil && !goja.IsUndefined(c) {
		return [2]int{int(c.ToInteger()), int(obj.Get("row").ToInteger())}, true
	}
	if a := obj.Get("0"); a != nil && !goja.IsUndefined(a) {
		return [2]int{int(a.ToInteger()), int(obj.Get("1").ToInteger())}, true
	}
	return [2]int{}, false
}

// call invokes one handler, converting exceptions and panics into log lines —
// a broken script must never kill the tick loop.
func (h *scriptHost) call(fn goja.Callable, ctx *ScriptCtx) {
	defer func() {
		if r := recover(); r != nil {
			log.Println("[script] handler panic:", r)
		}
	}()
	if _, err := fn(goja.Undefined(), h.vm.ToValue(ctx)); err != nil {
		log.Println("[script] handler error:", err)
	}
}

func (h *scriptHost) fireEnter(e *Engine, p *Player, col, row int) {
	ctx := &ScriptCtx{e: e, Player: p, Col: col, Row: row}
	for _, fn := range h.enterCell[[2]int{col, row}] {
		h.call(fn, ctx)
	}
	sym := p.CurrentMap.Grid[row][col]
	for _, fn := range h.enterTile[sym.WallID] {
		h.call(fn, ctx)
	}
	if sym.FloorID != sym.WallID {
		for _, fn := range h.enterTile[sym.FloorID] {
			h.call(fn, ctx)
		}
	}
}

// fireUse samples along the look direction at several depths (same trick as
// doors) and fires the first cell that has any handler registered.
func (h *scriptHost) fireUse(e *Engine, p *Player, dirX, dirY float64) {
	m := p.CurrentMap
	seen := map[[2]int]bool{}
	for _, t := range [...]float64{0.3, 0.5, 0.7, 1.0, 1.3} {
		col := int((p.X + dirX*m.TileSize*t) / m.TileSize)
		row := int((p.Y + dirY*m.TileSize*t) / m.TileSize)
		pos := [2]int{col, row}
		if seen[pos] || col < 0 || col >= m.Cols || row < 0 || row >= m.Rows {
			continue
		}
		seen[pos] = true
		fns := append([]goja.Callable{}, h.useCell[pos]...)
		fns = append(fns, h.useTile[m.Grid[row][col].WallID]...)
		if len(fns) == 0 {
			continue
		}
		ctx := &ScriptCtx{e: e, Player: p, Col: col, Row: row}
		for _, fn := range fns {
			h.call(fn, ctx)
		}
		return
	}
}

func (h *scriptHost) fireJoin(e *Engine, p *Player) {
	ctx := &ScriptCtx{e: e, Player: p, Col: p.prevCol, Row: p.prevRow}
	for _, fn := range h.joins {
		h.call(fn, ctx)
	}
}

func (h *scriptHost) fireTicks(e *Engine) {
	now := time.Now()
	for _, t := range h.ticks {
		if now.Sub(t.last) >= t.every {
			t.last = now
			h.call(t.fn, &ScriptCtx{e: e, Col: -1, Row: -1})
		}
	}
}
