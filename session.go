package main

import (
	"encoding/json"
	"log"
	"os"

	"github.com/gorilla/websocket"
)

type Session struct {
	wsConn *websocket.Conn
	pyConn *PythonClient
	input  map[string]bool
}

func (s *Session) run() {
	inputState := make(map[string]bool)
	for {
		_, msg, err := s.wsConn.ReadMessage()
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

		pngBytes, err := s.pyConn.SendInput(incoming)
		if err != nil {
			panic(err)
		}
		err = os.WriteFile("test_go.png", pngBytes, 0644)

	}
}
