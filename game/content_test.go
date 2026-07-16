package game

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// copyContent makes a throwaway copy of the repo's ./content tree (minus the
// large ost/ audio, which LoadContent never reads) so a test can mutate it
// without touching the real content.
func copyContent(t *testing.T) string {
	t.Helper()
	src := "../content"
	dst := t.TempDir()
	for _, name := range []string{
		"definitions.csv", "MUSIC_DEFS.csv", "TILES.csv", "MUSIC.csv", "hatman.gif",
		"textures", "scripts",
	} {
		if err := copyTree(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			t.Fatalf("seeding content copy: %v", err)
		}
	}
	return dst
}

func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// TestLoadContentRejectsBrokenMap is the validation gate: a malformed TILES.csv
// must make LoadContent fail so an admin sync can reject it before it ever goes
// live.
func TestLoadContentRejectsBrokenMap(t *testing.T) {
	dir := copyContent(t)
	// A cell with two layer IDs instead of three (ceiling/wall/floor).
	if err := os.WriteFile(filepath.Join(dir, "TILES.csv"), []byte("0100 0100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadContent(dir); err == nil {
		t.Fatal("expected LoadContent to reject a malformed TILES.csv, got nil error")
	}
}

// TestReloadContentRespawnsPlayers verifies a live reload swaps in a fresh base
// map, re-points every player's CurrentMap onto it, moves them to spawn, and
// tells the browser (popup + music).
func TestReloadContentRespawnsPlayers(t *testing.T) {
	dir := copyContent(t)
	e, err := NewEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	p := e.Join(1, "tester", nil)
	defer e.Leave(p)
	e.DrainEvents(p) // clear the join events

	oldMap := p.CurrentMap
	// Shove the player far from spawn so a successful respawn is unambiguous.
	p.X += 3 * TileSize
	p.Y += 3 * TileSize

	if err := e.ReloadContent(); err != nil {
		t.Fatalf("ReloadContent: %v", err)
	}

	newMap := e.world.Maps[0]
	if p.CurrentMap != newMap {
		t.Fatal("player CurrentMap was not re-pointed to the reloaded base map")
	}
	if p.CurrentMap == oldMap {
		t.Fatal("base map pointer did not change on reload")
	}
	sx, sy := findSpawn(newMap)
	if p.X != sx || p.Y != sy {
		t.Fatalf("player not respawned: got (%v,%v) want (%v,%v)", p.X, p.Y, sx, sy)
	}

	evs := drainTypes(t, e, p)
	if len(evs["popup"]) == 0 {
		t.Fatal("no reload popup emitted to the player")
	}
	if len(evs["music"]) == 0 {
		t.Fatal("no music event re-emitted after reload")
	}
}
