#!/usr/bin/env bash

cd "$(dirname "$0")/.." || exit

# Compile the plugin
GOEXPERIMENT=nosystemcrypto CGO_ENABLED=0 go build -o bin/cnpg-i-sidecar-injector main.go
