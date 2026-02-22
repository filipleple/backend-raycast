package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type Session struct {
	wsConn *websocket.Conn
	pyConn *PythonClient

	mu    sync.RWMutex
	input map[string]bool

	done chan struct{}
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

func (s *Session) tickLoop() {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	defer s.Cleanup()

	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
		}

		// snapshot input
		s.mu.RLock()
		snapshot := make(map[string]bool, len(s.input))
		for k, v := range s.input {
			snapshot[k] = v
		}
		s.mu.RUnlock()

		png, err := s.pyConn.SendInput(snapshot)
		if err != nil {
			return
		}

		//single writer: only tickLoop writes
		err = s.wsConn.WriteMessage(websocket.BinaryMessage, png)
		if err != nil {
			return
		}
	}
}

func (s *Session) Start() {
	go s.readWS()
	s.tickLoop()
}

func (s *Session) readWS() {
	defer close(s.done)

	for {
		_, msg, err := s.wsConn.ReadMessage()
		if err != nil {
			return
		}

		var incoming map[string]bool
		if err := json.Unmarshal(msg, &incoming); err != nil {
			continue
		}

		s.mu.Lock()
		for k, v := range incoming {
			s.input[k] = v
		}
		s.mu.Unlock()
	}
}

func (s *Session) Cleanup() {
	s.wsConn.Close()
	s.pyConn.Close()
}
