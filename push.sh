#!/bin/bash
set -e

: ${GHCR_USER:?please set GHCR_USER}
NS="ghcr.io/$GHCR_USER"

docker build -f Dockerfile.backend -t "$NS/raycast-go-backend:latest" .

docker push "$NS/raycast-go-backend:latest"

echo "pushed $NS/raycast-go-backend:latest"
