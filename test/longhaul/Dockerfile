# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Build stage
#
# IMPORTANT: This Dockerfile must be built with the REPOSITORY ROOT as the
# build context, not test/longhaul/, because test/longhaul/go.mod has
# `replace` directives pointing to ../shared and ../../operator/src.
#
#   docker build -f test/longhaul/Dockerfile -t <registry>/longhaul-test:latest .
#
FROM golang:1.25-alpine AS builder

# Note: the golang:*-alpine image already ships ca-certificates, and all
# deps in go.mod resolve via proxy.golang.org (no VCS fetches), so neither
# `git` nor an extra `ca-certificates` install is required here.

WORKDIR /src

# Copy the three modules referenced by test/longhaul/go.mod's replace directives.
# operator/src is the main operator module; test/shared is the shared helpers
# module consumed via `test/shared/{documentdb,mongo}`.
COPY operator/src ./operator/src
COPY test/shared ./test/shared
COPY test/longhaul ./test/longhaul

WORKDIR /src/test/longhaul

# Cache module downloads (after sources are in place so replace paths resolve).
RUN go mod download

# Build the standalone binary.
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /longhaul ./cmd/longhaul

# Runtime stage
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /longhaul /longhaul

USER nonroot:nonroot

ENTRYPOINT ["/longhaul"]
