package main

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	http.Handle("/", http.FileServer(http.Dir("./")))
	http.HandleFunc("/ws", handleWS)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}
	defer conn.Close()

	inputState := make(map[string]bool)

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("read error:", err)
			return
		}

		var incoming map[string]bool
		err = json.Unmarshal(msg, &incoming)
		if err != nil {
			log.Println("json error:", err)
			continue
		}

		for k, v := range incoming {
			inputState[k] = v
		}

		log.Printf("current input state: %+v\n", inputState)
	}
}
