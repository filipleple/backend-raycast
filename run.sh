#!/usr/bin/bash
set -e

# browser-side engine for static/game.html (see cmd/client)
GOOS=js GOARCH=wasm go build -ldflags=-s -o static/engine.wasm ./cmd/client
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" static/

go run .
