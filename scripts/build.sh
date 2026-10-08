#!/bin/bash
# Build the detector for the reMarkable 2 (32-bit ARM, static, no cgo).
set -e
cd "$(dirname "$0")/../detector"
go vet ./...
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o margin-todos-detector .
ls -l margin-todos-detector
