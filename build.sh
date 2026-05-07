#!/usr/bin/env bash
set -e

echo "Building frontend..."
npm run build -w shared
npm run build -w frontend

echo "Compiling Go binary (linux/amd64)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o rollarr ./cmd/rollarr/

echo "Done: ./rollarr"
