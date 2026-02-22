package main

import (
	"net"

	"github.com/gorilla/websocket"
)

type Session struct {
	wsConn *websocket.Conn
	pyConn net.Conn
	input  map[string]bool
}
