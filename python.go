package main

import (
	"encoding/json"
	"net"
)

type PythonClient struct {
	conn net.Conn
}

type InputMessage struct {
	PlayerID int             `json:"player_id"`
	Keys     map[string]bool `json:"keys"`
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

func (p *PythonClient) SendHandshake(playerID int, avatarBytes []byte) error {
	handshake := struct {
		PlayerID int `json:"player_id"`
	}{PlayerID: playerID}

	jsonBytes, err := json.Marshal(handshake)
	if err != nil {
		return err
	}
	if err := sendFrame(p.conn, jsonBytes); err != nil {
		return err
	}

	if avatarBytes == nil {
		avatarBytes = []byte{}
	}
	return sendFrame(p.conn, avatarBytes)
}

func (p *PythonClient) SendInput(msg InputMessage) ([]byte, error) {
	jsonBytes, err := json.Marshal(msg)
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
