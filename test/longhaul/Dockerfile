# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Build stage
FROM golang:1.26-alpine AS builder

# Note: the golang:*-alpine image already ships ca-certificates, and all
# deps in go.mod resolve via proxy.golang.org (no VCS fetches), so neither
# `git` nor an extra `ca-certificates` install is required here.

WORKDIR /src

# Cache module downloads.
COPY go.mod go.sum ./
RUN go mod download

# Copy source.
COPY . .

# Build the standalone binary.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /longhaul ./cmd/longhaul

# Runtime stage
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /longhaul /longhaul

USER nonroot:nonroot

ENTRYPOINT ["/longhaul"]
