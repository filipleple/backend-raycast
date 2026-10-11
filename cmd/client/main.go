//go:build js && wasm

// Command client is the browser-side engine: game.Client compiled to
// WebAssembly and exposed to static/game.html as the global `raycast`.
// Build: GOOS=js GOARCH=wasm go build -o static/engine.wasm ./cmd/client
package main

import (
	"syscall/js"

	"raycast/game"
)

// simKeys are the only keys movement prediction looks at.
var simKeys = []string{"ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "a", "d"}

func main() {
	c := game.NewClient()
	api := js.Global().Get("Object").New()
	fn := func(name string, f func(args []js.Value) any) {
		api.Set(name, js.FuncOf(func(_ js.Value, args []js.Value) any { return f(args) }))
	}

	// apply(json) -> null | error string
	fn("apply", func(a []js.Value) any {
		if err := c.Apply([]byte(a[0].String())); err != nil {
			return err.Error()
		}
		return nil
	})
	// version() -> content version string
	fn("version", func(a []js.Value) any { return c.Version() })
	// wanted() -> [path, ...] to fetch from /content/
	fn("wanted", func(a []js.Value) any {
		w := c.Wanted()
		out := make([]any, len(w))
		for i, p := range w {
			out[i] = p
		}
		return out
	})
	// addFile(path, Uint8Array)
	fn("addFile", func(a []js.Value) any {
		c.AddFile(a[0].String(), bytesOf(a[1]))
		return nil
	})
	// setAvatar(id, Uint8Array)
	fn("setAvatar", func(a []js.Value) any {
		c.SetAvatar(a[0].Int(), bytesOf(a[1]))
		return nil
	})
	// step(x, y, angle, keys) -> [x, y, angle]
	fn("step", func(a []js.Value) any {
		keys := map[string]bool{}
		for _, k := range simKeys {
			keys[k] = a[3].Get(k).Truthy()
		}
		x, y, ang := c.Step(a[0].Float(), a[1].Float(), a[2].Float(), keys)
		return []any{x, y, ang}
	})
	// render(x, y, angle, showMap, others Float64Array [id,x,y,...], out Uint8ClampedArray) -> bool
	fn("render", func(a []js.Value) any {
		others := make([]float64, a[4].Length())
		for i := range others {
			others[i] = a[4].Index(i).Float()
		}
		pix := c.Render(a[0].Float(), a[1].Float(), a[2].Float(), a[3].Truthy(), others)
		if pix == nil {
			return false
		}
		js.CopyBytesToJS(a[5], pix)
		return true
	})

	js.Global().Set("raycast", api)
	if ready := js.Global().Get("onRaycastReady"); ready.Type() == js.TypeFunction {
		ready.Invoke()
	}
	select {} // keep the exported functions alive
}

func bytesOf(v js.Value) []byte {
	b := make([]byte, v.Length())
	js.CopyBytesToGo(b, v)
	return b
}
