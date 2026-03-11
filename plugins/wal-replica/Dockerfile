# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Step 1: build image
FROM golang:1.24 AS builder

# Cache the dependencies
WORKDIR /app
COPY go.mod go.sum /app/
RUN go mod download

# Compile the application
COPY . /app
RUN --mount=type=cache,target=/root/.cache/go-build ./scripts/build.sh

# Step 2: build the image to be actually run
FROM golang:1-alpine
USER 10001:10001
COPY --from=builder /app/bin/cnpg-i-wal-replica /app/bin/cnpg-i-wal-replica
ENTRYPOINT ["/app/bin/cnpg-i-wal-replica"]
