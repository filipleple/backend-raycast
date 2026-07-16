package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"raycast/game"
)

// seedContentRepo makes a temp copy of ./content (minus the large ost/ audio),
// git-inits it with one commit, and returns an adminServer wired to it. No DB —
// only the git/validation/reload methods are under test here.
func seedContentRepo(t *testing.T) *adminServer {
	t.Helper()
	dst := t.TempDir()
	for _, name := range []string{
		"definitions.csv", "MUSIC_DEFS.csv", "TILES.csv", "MUSIC.csv", "hatman.gif",
		"textures", "scripts",
	} {
		if err := copyTree(filepath.Join("content", name), filepath.Join(dst, name)); err != nil {
			t.Fatalf("seeding: %v", err)
		}
	}
	engine, err := game.NewEngine(dst)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	a := newAdminServer(nil, engine, dst)
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=seed", "-c", "user.email=s@x", "commit", "-qm", "initial"},
	} {
		if out, err := a.git(args...); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	return a
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

// TestValidateWithNewCSVs exercises the sync validation gate: the current CSVs
// re-offered must pass, a malformed one must be rejected — all against the real
// assets via the symlink staging tree.
func TestValidateWithNewCSVs(t *testing.T) {
	a := seedContentRepo(t)

	good := map[string][]byte{}
	for _, f := range csvTargets {
		b, err := os.ReadFile(filepath.Join(a.contentAbs, f))
		if err != nil {
			t.Fatal(err)
		}
		good[f] = b
	}
	if err := a.validateWithNewCSVs(good); err != nil {
		t.Fatalf("current CSVs should validate, got: %v", err)
	}

	bad := map[string][]byte{}
	for k, v := range good {
		bad[k] = v
	}
	bad["TILES.csv"] = []byte("0100 0100\n") // two layers, not three
	if err := a.validateWithNewCSVs(bad); err == nil {
		t.Fatal("expected a malformed TILES.csv to be rejected")
	}
}

// TestRollbackDeletesLaterFiles is the regression guard for the rollback bug:
// a file added after the target revision must be GONE after rolling back to it,
// and the rollback must land as a new commit on top of history.
func TestRollbackDeletesLaterFiles(t *testing.T) {
	a := seedContentRepo(t)
	admin := User{Username: "admin"}

	initSHA, err := a.git("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	initSHA = strings.TrimSpace(initSHA)

	// Commit a new file (as an upload would).
	added := filepath.Join(a.contentAbs, "textures", "walls", "added.txt")
	if err := writeFileAtomic(added, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.gitCommit(admin, "add file"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(added); err != nil {
		t.Fatalf("added file should exist pre-rollback: %v", err)
	}

	if err := a.gitRollback(admin, initSHA); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	if _, err := os.Stat(added); !os.IsNotExist(err) {
		t.Fatal("rollback did not delete the file added after the target revision")
	}
	// HEAD is a fresh commit (not the old tip, not the target itself) and the
	// tree matches the target.
	head, _ := a.git("rev-parse", "HEAD")
	if strings.TrimSpace(head) == initSHA {
		t.Fatal("rollback should create a new commit, not move HEAD onto the target")
	}
	if out, _ := a.git("status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("working tree should be clean after rollback, got:\n%s", out)
	}
}
