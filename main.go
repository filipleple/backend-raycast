package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"path/filepath"
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

	// Optionally promote a bootstrap admin on startup (idempotent).
	if u := os.Getenv("ADMIN_USERNAME"); u != "" {
		if _, err := db.Exec("UPDATE users SET is_admin = true WHERE username = $1", u); err != nil {
			log.Printf("admin bootstrap for %q failed: %v", u, err)
		} else {
			log.Printf("ensured %q is admin", u)
		}
	}

	// auth routes
	http.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		loginHandle(w, r, db)
	})
	http.HandleFunc("/logout", func(w http.ResponseWriter, r *http.Request) {
		logoutHandle(w, r, db)
	})
	http.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		registerHandle(w, r, db)
	})
	http.HandleFunc("/unregister", func(w http.ResponseWriter, r *http.Request) {
		unregisterHandle(w, r, db)
	})
	http.HandleFunc("/avatar/upload", func(w http.ResponseWriter, r *http.Request) {
		uploadAvatarHandle(w, r, db)
	})
	http.HandleFunc("/avatar", func(w http.ResponseWriter, r *http.Request) {
		avatarHandle(w, r, db)
	})
	http.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		meHandle(w, r, db)
	})

	// admin panel (content sync + live reload); gated to admin users inside
	newAdminServer(db, engine, contentDir).routes()

	//game routes
	http.Handle("/", http.FileServer(http.Dir("./static/")))
	http.Handle("/ost/", http.StripPrefix("/ost/", http.FileServer(http.Dir(filepath.Join(contentDir, "ost")))))

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, db, engine)
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleWS(w http.ResponseWriter, r *http.Request, db *sql.DB, engine *game.Engine) {
	user, err := getUserBySessionCookie(db, r)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{Error: "not logged in"})
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

	avatarBytes, _, _, err := getAvatar(db, user.Username)
	if err != nil {
		avatarBytes = nil
	}
	player := engine.Join(user.ID, user.Username, avatarBytes)

	session := &Session{
		wsConn:   ws,
		engine:   engine,
		player:   player,
		input:    make(map[string]bool),
		done:     make(chan struct{}),
		userID:   user.ID,
		username: user.Username,
	}

	activeSessionsMu.Lock()
	activeSessions[user.ID] = session
	activeSessionsMu.Unlock()

	session.Start()
}
