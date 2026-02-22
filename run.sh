#!/usr/bin/bash

trap 'kill 0' EXIT

python3 ./renderer/render.py &
go run .
