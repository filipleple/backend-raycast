package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"raycast/game"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

var (
	activeSessionsMu sync.Mutex
	activeSessions   = map[int]*Session{}
)

func main() {
	db := connectPostgresDB()
	defer db.Close()

	// Content (maps, defs, music, textures, scripts) is read from CONTENT_DIR,
	// a git-versioned directory mounted at runtime rather than baked into the
	// image — so the admin panel can sync + hot-reload it without a redeploy.
	// Defaults to ./content so bare `go run .` works from the repo root.
	contentDir := os.Getenv("CONTENT_DIR")
	if contentDir == "" {
		contentDir = "./content"
	}

	engine, err := game.NewEngine(contentDir)
	if err != nil {
		log.Fatal("loading game assets: ", err)
	}

	// auth routes: accounts are 629fm's (629Manager), see auth.go
	http.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		sessionHandle(w, r, db)
	})
	http.HandleFunc("/avatar/upload", func(w http.ResponseWriter, r *http.Request) {
		uploadAvatarHandle(w, r, db)
	})
	http.HandleFunc("/avatar", func(w http.ResponseWriter, r *http.Request) {
		avatarHandle(w, r, db)
	})

	// admin panel (content sync + live reload); gated to admin users inside
	newAdminServer(db, engine, contentDir).routes()

	//game routes
	http.Handle("/", http.FileServer(http.Dir("./static/")))
	http.Handle("/ost/", http.StripPrefix("/ost/", http.FileServer(http.Dir(filepath.Join(contentDir, "ost")))))

	// textures for browser-side engines, and other players' avatars
	http.Handle("/content/", engine.ContentHandler("/content/"))
	http.HandleFunc("/avatar/player/", func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/avatar/player/"))
		data := engine.PlayerAvatar(id)
		if err != nil || data == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", http.DetectContentType(data))
		w.Write(data)
	})

	// full-screen picture viewer: /picture/<picture>/<frame>.png
	http.HandleFunc("/picture/", func(w http.ResponseWriter, r *http.Request) {
		picture, frame, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/picture/"), "/")
		frame, isPNG := strings.CutSuffix(frame, ".png")
		if !ok || !isPNG {
			http.NotFound(w, r)
			return
		}
		data, err := engine.PictureViewPNG(picture, frame)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-cache") // content reloads may replace it
		w.Write(data)
	})

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, db, engine)
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleWS(w http.ResponseWriter, r *http.Request, db *sql.DB, engine *game.Engine) {
	if !sameOrigin(r) { // the cookie alone must not let another site open a session
		writeJSON(w, http.StatusForbidden, ErrorResponse{Error: "cross-origin request refused"})
		return
	}
	user, err := getUserBySessionCookie(db, r)
	if err != nil {
		writeAuthError(w, err)
		return
	}

	activeSessionsMu.Lock()
	if _, alreadyIn := activeSessions[user.ID]; alreadyIn {
		activeSessionsMu.Unlock()
		writeJSON(w, http.StatusConflict, ErrorResponse{Error: "already connected"})
		return
	}
	activeSessions[user.ID] = nil // reserve slot
	activeSessionsMu.Unlock()

	defer func() {
		activeSessionsMu.Lock()
		delete(activeSessions, user.ID)
		activeSessionsMu.Unlock()
	}()

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}

	avatarBytes, _, _, err := getAvatar(db, user.ID)
	if err != nil {
		avatarBytes = nil
	}
	player := engine.Join(user.ID, user.Username, avatarBytes)

	session := &Session{
		// ?engine=client: the browser renders (static/game.html); without it
		// the server streams JPEG frames (static/game-jpeg.html)
		clientEngine: r.URL.Query().Get("engine") == "client",
		wsConn:       ws,
		engine:       engine,
		player:       player,
		input:        make(map[string]bool),
		done:         make(chan struct{}),
		userID:       user.ID,
		username:     user.Username,
	}

	activeSessionsMu.Lock()
	activeSessions[user.ID] = session
	activeSessionsMu.Unlock()

	session.Start()
}
