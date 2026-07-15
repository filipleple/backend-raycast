package main

import (
	"encoding/json"
	"log"
	"sync"
	"time"

	"raycast/game"

	"github.com/gorilla/websocket"
)

type Session struct {
	wsConn *websocket.Conn
	engine *game.Engine
	player *game.Player

	mu    sync.RWMutex
	input map[string]bool

	done     chan struct{}
	userID   int
	username string
}

func (s *Session) tickLoop() {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer s.Cleanup()
	// scripts are allowed to wreck a player's state badly enough that
	// rendering panics; that kicks this player, not the server
	defer func() {
		if r := recover(); r != nil {
			log.Printf("session for %s died: %v", s.username, r)
		}
	}()

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

		frame, events, err := s.engine.Tick(s.player, snapshot)
		if err != nil {
			return
		}

		// text frames carry JSON control events (music, popups, sounds);
		// binary frames stay JPEG video — the browser tells them apart for
		// free in onmessage
		s.wsConn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
		if events != nil {
			if err := s.wsConn.WriteMessage(websocket.TextMessage, events); err != nil {
				return
			}
		}
		err = s.wsConn.WriteMessage(websocket.BinaryMessage, frame)
		s.wsConn.SetWriteDeadline(time.Time{})
		if err != nil {
			return
		}
	}
}

func (s *Session) Start() {
	log.Println("starting a player session")
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
	s.engine.Leave(s.player)
}
