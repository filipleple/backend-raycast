package main

import (
	"encoding/json"
	"net"
)

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
