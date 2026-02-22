package main

import (
	"encoding/json"
	"log"
	"net"
	"os"
	"time"
)

func tickRenderer() {
	conn, err := net.Dial("tcp", "127.0.0.1:9000")
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	tick := 0

	for range ticker.C {
		tick++

		input := map[string]bool{"right": true}
		jsonBytes, err := json.Marshal(input)
		if err != nil {
			panic(err)
		}

		err = sendFrame(conn, jsonBytes)
		if err != nil {
			panic(err)
		}

		pngBytes, err := recvBinary(conn)
		if err != nil {
			panic(err)
		}

		err = os.WriteFile("test_go.png", pngBytes, 0644)

		log.Printf("tick=%d bytes=%d\n", tick, len(pngBytes))
	}
}
