#!/usr/bin/env bash

cd "$(dirname "$0")/.." || exit

# Compile the plugin
CGO_ENABLED=0 go build -gcflags="all=-N -l" -o bin/cnpg-i-wal-replica main.go
