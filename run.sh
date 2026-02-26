#!/usr/bin/bash

trap 'kill 0' EXIT

python3 ./renderer/main.py &
go run .
