package main

import (
	"database/sql"
	"log"
	"net/http"
	"os"
	"sync"

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

	//game routes
	http.Handle("/", http.FileServer(http.Dir("./static/")))

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, db)
	})

	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleWS(w http.ResponseWriter, r *http.Request, db *sql.DB) {
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

	// allow hosting the renderer at different addresses
	pyAddr := os.Getenv("RENDERER_ADDR")
	if pyAddr == "" {
		pyAddr = "127.0.0.1:9000"
	}
	log.Println("connecting to the renderer at: ", pyAddr)
	pythonClient, err := NewPythonClient(pyAddr)
	if err != nil {
		log.Println("connecting to the renderer failed")
		ws.Close()
		return
	}

	avatarBytes, _, _, err := getAvatar(db, user.Username)
	if err != nil {
		avatarBytes = nil
	}
	if err := pythonClient.SendHandshake(user.ID, avatarBytes); err != nil {
		ws.Close()
		pythonClient.Close()
		return
	}

	session := &Session{
		wsConn:   ws,
		pyConn:   pythonClient,
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
