package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"raycast/game"
)

// TestClientEngineE2E builds the browser engine to WebAssembly and runs it
// under Node against a real client-engine Session (no database: the auth
// step is bypassed). Skipped with -short or when node isn't installed.
func TestClientEngineE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("-short")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}

	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "engine.wasm"), "./cmd/client")
	build.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building engine.wasm: %v\n%s", err, out)
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	wasmExec, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(goroot)), "lib", "wasm", "wasm_exec.js"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wasm_exec.js"), wasmExec, 0o644); err != nil {
		t.Fatal(err)
	}

	engine, err := game.NewEngine("./content")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/content/", engine.ContentHandler("/content/"))
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		s := &Session{
			clientEngine: r.URL.Query().Get("engine") == "client",
			wsConn:       ws,
			engine:       engine,
			player:       engine.Join(7, "e2e", nil),
			input:        map[string]bool{},
			done:         make(chan struct{}),
			username:     "e2e",
		}
		s.Start()
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := exec.Command(node, "testdata/e2e_client.js", dir, srv.URL)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
}
