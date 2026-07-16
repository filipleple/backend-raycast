package main

import (
	"bytes"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"raycast/game"
)

//go:embed templates/admin.html
var adminHTML []byte

// csvTargets are the four content files sourced from Google Sheets tabs, in a
// fixed order. Each maps to one row in the content_sources table.
var csvTargets = []string{"definitions.csv", "TILES.csv", "MUSIC.csv", "MUSIC_DEFS.csv"}

// assetKinds maps an upload "kind" to the content subdirectory it lands in and
// the content types allowed for it. Textures are sniffed; audio is checked by
// sniff prefix (browsers/ffprobe-free).
var assetKinds = map[string]struct {
	subdir string
	allow  func(mime string) bool
}{
	"walls":           {"textures/walls", isImage},
	"floors+ceilings": {"textures/floors+ceilings", isImage},
	"door":            {"textures/door", isImage},
	"sprites":         {"textures/sprites", isImage},
	"ost":             {"ost", isAudio},
}

func isImage(m string) bool { return m == "image/png" || m == "image/gif" }
func isAudio(m string) bool {
	return strings.HasPrefix(m, "audio/") || m == "application/ogg" || m == "application/octet-stream"
}

// adminServer bundles the dependencies the /admin/* handlers share. The content
// directory doubles as a git working tree — each sync/upload/rollback is a
// commit, giving diff/history/pin/revert for free.
type adminServer struct {
	db         *sql.DB
	engine     *game.Engine
	contentAbs string
}

func newAdminServer(db *sql.DB, engine *game.Engine, contentDir string) *adminServer {
	abs, err := filepath.Abs(contentDir)
	if err != nil {
		abs = contentDir
	}
	return &adminServer{db: db, engine: engine, contentAbs: abs}
}

func (a *adminServer) routes() {
	http.HandleFunc("/admin", a.page)
	http.HandleFunc("/admin/status", a.status)
	http.HandleFunc("/admin/sources", a.sources)
	http.HandleFunc("/admin/sync/sheets", a.syncSheets)
	http.HandleFunc("/admin/assets/upload", a.uploadAsset)
	http.HandleFunc("/admin/revisions", a.revisions)
	http.HandleFunc("/admin/rollback", a.rollback)
}

// ---- guards --------------------------------------------------------------

// requireAdmin enforces a logged-in admin. It writes the error response itself
// and returns ok=false when the caller should stop.
func (a *adminServer) requireAdmin(w http.ResponseWriter, r *http.Request) (User, bool) {
	user, err := getUserBySessionCookie(a.db, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "not logged in"})
		return User{}, false
	}
	if !user.IsAdmin {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "admin only"})
		return User{}, false
	}
	return user, true
}

// sameOrigin is a lightweight CSRF guard for state-changing admin routes: a
// cross-site POST from a browser carries an Origin/Referer whose host differs
// from ours. Non-browser clients (no such header) are allowed — they already
// need the admin session cookie.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		o = r.Header.Get("Referer")
	}
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

func (a *adminServer) guardWrite(w http.ResponseWriter, r *http.Request) (User, bool) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return User{}, false
	}
	user, ok := a.requireAdmin(w, r)
	if !ok {
		return User{}, false
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "cross-origin request refused"})
		return User{}, false
	}
	return user, true
}

// ---- handlers ------------------------------------------------------------

func (a *adminServer) page(w http.ResponseWriter, r *http.Request) {
	// This is a browser navigation, not an API call: send non-admins to the
	// front page rather than writing a JSON error (the /admin/* API endpoints
	// return proper 401/403).
	user, err := getUserBySessionCookie(a.db, r)
	if err != nil || !user.IsAdmin {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(adminHTML)
}

func (a *adminServer) status(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	head := ""
	if a.gitRepo() {
		if out, err := a.git("rev-parse", "HEAD"); err == nil {
			head = strings.TrimSpace(out)
		}
	}
	activeSessionsMu.Lock()
	players := make([]string, 0, len(activeSessions))
	for _, s := range activeSessions {
		if s != nil {
			players = append(players, s.username)
		}
	}
	activeSessionsMu.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"content_dir": a.contentAbs,
		"git":         a.gitRepo(),
		"head":        head,
		"players":     players,
	})
}

// sources: GET returns the configured sheet id/gid per file; POST upserts them.
func (a *adminServer) sources(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if _, ok := a.requireAdmin(w, r); !ok {
			return
		}
		srcs, err := a.getSources()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"targets": csvTargets, "sources": srcs})
	case http.MethodPost:
		if _, ok := a.guardWrite(w, r); !ok {
			return
		}
		var body map[string]sheetSource
		if err := readJSON(r, &body); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid json"})
			return
		}
		for _, f := range csvTargets {
			s := body[f]
			if err := a.setSource(f, s.SheetID, s.Gid); err != nil {
				writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, MessageResponse{Message: "sources saved"})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use GET or POST"})
	}
}

func (a *adminServer) syncSheets(w http.ResponseWriter, r *http.Request) {
	user, ok := a.guardWrite(w, r)
	if !ok {
		return
	}

	srcs, err := a.getSources()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	// Fetch every tab first — a failure here means nothing is touched on disk.
	newFiles := map[string][]byte{}
	for _, f := range csvTargets {
		s := srcs[f]
		if s.SheetID == "" || s.Gid == "" {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("no sheet source configured for %s — set it under Sources first", f)})
			return
		}
		data, err := fetchSheetCSV(s.SheetID, s.Gid)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, ErrorResponse{Error: fmt.Sprintf("fetching %s: %v", f, err)})
			return
		}
		newFiles[f] = data
	}

	// Validation gate: load the new CSVs against the current assets in a throwaway
	// staging tree. If anything fails to parse, bail — live content is untouched.
	if err := a.validateWithNewCSVs(newFiles); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Error: fmt.Sprintf("new content rejected (nothing changed): %v", err)})
		return
	}

	if err := a.writeFiles(newFiles); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	commitMsg := fmt.Sprintf("sync from sheets %s", time.Now().Format(time.RFC3339))
	sha, gitErr := a.gitCommit(user, commitMsg)

	if err := a.engine.ReloadContent(); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("written but reload failed: %v", err)})
		return
	}

	resp := map[string]any{"message": "synced and reloaded", "revision": sha}
	if gitErr != nil {
		resp["warning"] = "content is live but not committed: " + gitErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *adminServer) uploadAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, ErrorResponse{Error: "use POST"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 20<<20) // 20 MB
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid multipart form or file too large"})
		return
	}
	user, ok := a.requireAdmin(w, r)
	if !ok {
		return
	}
	if !sameOrigin(r) {
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "cross-origin request refused"})
		return
	}

	kind := r.FormValue("kind")
	spec, known := assetKinds[kind]
	if !known {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "unknown asset kind"})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "missing file"})
		return
	}
	defer file.Close()

	name := filepath.Base(header.Filename)
	if name == "" || name == "." || strings.ContainsAny(name, `/\`) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "bad filename"})
		return
	}

	head := make([]byte, 512)
	hn, _ := file.Read(head)
	mime := http.DetectContentType(head[:hn])
	if !spec.allow(mime) {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: fmt.Sprintf("content type %q not allowed for kind %q", mime, kind)})
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: "cannot read file"})
		return
	}
	data, err := io.ReadAll(file)
	if err != nil || len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "empty or unreadable file"})
		return
	}

	dir := filepath.Join(a.contentAbs, filepath.FromSlash(spec.subdir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	if err := writeFileAtomic(filepath.Join(dir, name), data); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}

	sha, gitErr := a.gitCommit(user, fmt.Sprintf("upload %s/%s", spec.subdir, name))
	// Reload so a newly referenced texture is picked into live maps. Audio is
	// served statically and needs no reload, but reloading is harmless.
	if err := a.engine.ReloadContent(); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("saved but reload failed: %v", err)})
		return
	}
	resp := map[string]any{"message": fmt.Sprintf("uploaded %s/%s", spec.subdir, name), "revision": sha}
	if gitErr != nil {
		resp["warning"] = "saved but not committed: " + gitErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *adminServer) revisions(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.requireAdmin(w, r); !ok {
		return
	}
	revs, err := a.gitLog(50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"git": a.gitRepo(), "revisions": revs})
}

func (a *adminServer) rollback(w http.ResponseWriter, r *http.Request) {
	user, ok := a.guardWrite(w, r)
	if !ok {
		return
	}
	var body struct {
		SHA string `json:"sha"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.SHA) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "sha required"})
		return
	}
	if err := a.gitRollback(user, strings.TrimSpace(body.SHA)); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, ErrorResponse{Error: err.Error()})
		return
	}
	if err := a.engine.ReloadContent(); err != nil {
		writeJSON(w, http.StatusInternalServerError, ErrorResponse{Error: fmt.Sprintf("rolled back but reload failed: %v", err)})
		return
	}
	writeJSON(w, http.StatusOK, MessageResponse{Message: "rolled back and reloaded"})
}

// ---- content sources (DB) ------------------------------------------------

type sheetSource struct {
	SheetID string `json:"sheet_id"`
	Gid     string `json:"gid"`
}

func (a *adminServer) getSources() (map[string]sheetSource, error) {
	rows, err := a.db.Query("SELECT filename, sheet_id, gid FROM content_sources")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]sheetSource{}
	for rows.Next() {
		var f string
		var s sheetSource
		if err := rows.Scan(&f, &s.SheetID, &s.Gid); err != nil {
			return nil, err
		}
		out[f] = s
	}
	return out, rows.Err()
}

func (a *adminServer) setSource(filename, sheetID, gid string) error {
	_, err := a.db.Exec(`
		INSERT INTO content_sources(filename, sheet_id, gid) VALUES($1, $2, $3)
		ON CONFLICT (filename) DO UPDATE SET sheet_id = EXCLUDED.sheet_id, gid = EXCLUDED.gid`,
		filename, sheetID, gid)
	return err
}

// ---- staging / validation ------------------------------------------------

// validateWithNewCSVs loads the proposed CSVs against the current on-disk
// assets in a temporary tree of symlinks (so no multi-MB texture/audio copy).
// A nil return means the new content parses cleanly and is safe to activate.
func (a *adminServer) validateWithNewCSVs(newFiles map[string][]byte) error {
	staging, err := os.MkdirTemp("", "content-stage-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	entries, err := os.ReadDir(a.contentAbs)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, replacing := newFiles[e.Name()]; replacing || e.Name() == ".git" {
			continue
		}
		_ = os.Symlink(filepath.Join(a.contentAbs, e.Name()), filepath.Join(staging, e.Name()))
	}
	for name, data := range newFiles {
		if err := os.WriteFile(filepath.Join(staging, name), data, 0o644); err != nil {
			return err
		}
	}
	_, err = game.LoadContent(staging)
	return err
}

func (a *adminServer) writeFiles(files map[string][]byte) error {
	for name, data := range files {
		if err := writeFileAtomic(filepath.Join(a.contentAbs, name), data); err != nil {
			return err
		}
	}
	return nil
}

func writeFileAtomic(dst string, data []byte) error {
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// ---- git -----------------------------------------------------------------

func (a *adminServer) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = a.contentAbs
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return string(out), nil
}

func (a *adminServer) gitRepo() bool {
	_, err := a.git("rev-parse", "--is-inside-work-tree")
	return err == nil
}

// gitCommit stages everything and commits under the admin's authorship. Returns
// ("", nil) when the content dir is not a git repo (versioning simply
// disabled) or when there is nothing to commit.
func (a *adminServer) gitCommit(user User, message string) (string, error) {
	if !a.gitRepo() {
		return "", nil
	}
	if _, err := a.git("add", "-A"); err != nil {
		return "", err
	}
	if out, err := a.git("status", "--porcelain"); err == nil && strings.TrimSpace(out) == "" {
		return "", nil // nothing changed
	}
	author := fmt.Sprintf("%s <%s@raycast.local>", user.Username, user.Username)
	if _, err := a.git(
		"-c", "user.name=raycast-admin", "-c", "user.email=admin@raycast.local",
		"commit", "-m", message, "--author", author,
	); err != nil {
		return "", err
	}
	sha, err := a.git("rev-parse", "HEAD")
	return strings.TrimSpace(sha), err
}

type revision struct {
	SHA     string `json:"sha"`
	Author  string `json:"author"`
	When    int64  `json:"when"`
	Subject string `json:"subject"`
	Active  bool   `json:"active"`
}

func (a *adminServer) gitLog(n int) ([]revision, error) {
	if !a.gitRepo() {
		return nil, nil
	}
	head := ""
	if out, err := a.git("rev-parse", "HEAD"); err == nil {
		head = strings.TrimSpace(out)
	}
	out, err := a.git("log", "-n"+strconv.Itoa(n), "--pretty=format:%H%x1f%an%x1f%ct%x1f%s")
	if err != nil {
		return nil, err
	}
	var revs []revision
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\x1f")
		if len(parts) != 4 {
			continue
		}
		when, _ := strconv.ParseInt(parts[2], 10, 64)
		revs = append(revs, revision{
			SHA: parts[0], Author: parts[1], When: when, Subject: parts[3],
			Active: parts[0] == head,
		})
	}
	return revs, nil
}

// gitRollback makes the content tree exactly match sha — including deleting
// files that were added afterwards — validates it loads, then records the
// restore as a NEW commit on top of the current branch (full history is kept;
// no detached HEAD, no discarded commits). read-tree -u --reset is what makes
// the match exact; a plain `checkout <sha> -- .` would leave later-added files
// behind.
func (a *adminServer) gitRollback(user User, sha string) error {
	if !a.gitRepo() {
		return fmt.Errorf("content dir is not a git repo; rollback unavailable")
	}
	if _, err := a.git("cat-file", "-e", sha+"^{commit}"); err != nil {
		return fmt.Errorf("unknown revision %q", sha)
	}
	if _, err := a.git("read-tree", "-u", "--reset", sha); err != nil {
		return err
	}
	if _, err := game.LoadContent(a.contentAbs); err != nil {
		_, _ = a.git("read-tree", "-u", "--reset", "HEAD") // undo — restore current tip
		return fmt.Errorf("revision %s does not load cleanly: %w", shortSHA(sha), err)
	}
	if _, err := a.gitCommit(user, "rollback to "+shortSHA(sha)); err != nil {
		return err
	}
	return nil
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
