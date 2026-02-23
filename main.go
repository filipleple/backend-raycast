package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	http.Handle("/", http.FileServer(http.Dir("./static/")))
	http.HandleFunc("/ws", handleWS)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}

	pythonClient, err := NewPythonClient("127.0.0.1:9000")
	if err != nil {
		ws.Close()
		return
	}

	session := &Session{
		wsConn: ws,
		pyConn: pythonClient,
		input:  make(map[string]bool),
		done:   make(chan struct{}),
	}

	session.Start()
}
