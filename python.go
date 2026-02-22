package main

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"time"
)

func tickRenderer() {
	pythonClient, err := NewPythonClient("127.0.0.1:9000")
	if err != nil {
		panic(err)
	}

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	tick := 0

	for range ticker.C {
		tick++
		input := map[string]bool{"right": true}
		pngBytes, err := pythonClient.SendInput(input)
		if err != nil {
			panic(err)
		}

		err = os.WriteFile("test_go.png", pngBytes, 0644)

		log.Printf("tick=%d bytes=%d\n", tick, len(pngBytes))
	}
}

type PythonClient struct {
	conn net.Conn
}

func NewPythonClient(addr string) (*PythonClient, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	newClient := new(PythonClient)
	newClient.conn = conn

	return newClient, nil
}

func (p *PythonClient) SendInput(input map[string]bool) ([]byte, error) {
	jsonBytes, err := json.Marshal(input)
	if err != nil {
		panic(err)
	}

	err = sendFrame(p.conn, jsonBytes)
	if err != nil {
		return nil, err
	}

	pngBytes, err := recvBinary(p.conn)
	if err != nil {
		return nil, err
	}

	return pngBytes, nil
}

func (p *PythonClient) Close() {
	p.conn.Close()
}
