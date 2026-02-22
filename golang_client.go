package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
)

// [*] connect to socket
// [*] build a frame of given length, with given payload
// [*] parse the frame as event json
// [*] send the frame over a socket
// [*] recieve frame of given length
// [ ] probe the renderer and save received png

func recvExact(conn net.Conn, length int) ([]byte, error) {
	buf := make([]byte, length)
	_, err := io.ReadFull(conn, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func recvFrameBytes(conn net.Conn) ([]byte, error) {
	header, err := recvExact(conn, 4)
	if err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	frame_bytes, err := recvExact(conn, int(length))

	return frame_bytes, err
}

func recvJSON(conn net.Conn) (map[string]bool, error) {
	data, err := recvFrameBytes(conn)
	if err != nil {
		return nil, err
	}

	var inputs map[string]bool
	if err := json.Unmarshal(data, &inputs); err != nil {
		return nil, err
	}

	return inputs, nil
}

func recvBinary(conn net.Conn) ([]byte, error) {
	return recvFrameBytes(conn)
}

func sendFrame(conn net.Conn, payload []byte) error {
	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(payload)))

	if err := writeFull(conn, header); err != nil {
		return err
	}

	if err := writeFull(conn, payload); err != nil {
		return err
	}

	return nil
}

func writeFull(conn net.Conn, buf []byte) error {
	for len(buf) > 0 {
		n, err := conn.Write(buf)
		if err != nil {
			return err
		}
		buf = buf[n:]
	}
	return nil
}

func probeRenderer() error {
	msg := map[string]bool{
		"right": true,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	conn, err := net.Dial("tcp", "127.0.0.1:9000")
	if err != nil {
		return err
	}
	defer conn.Close()

	var png []byte

	for i := 0; i < 10; i++ {
		if err := sendFrame(conn, data); err != nil {
			return err
		}
		if png, err = recvBinary(conn); err != nil {
			return err
		}
	}

	err = os.WriteFile("test_go.png", png, 0644)
	if err != nil {
		return err
	}
	return nil
}

func main() {
	probeRenderer()
}
