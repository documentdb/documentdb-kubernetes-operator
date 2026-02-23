# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Step 1: build image
ARG TARGETOS
ARG TARGETARCH
ARG ARCH
FROM --platform=${TARGETOS:-linux}/${ARCH:-${TARGETARCH}} mcr.microsoft.com/oss/go/microsoft/golang:1.25-azurelinux3.0 AS builder


# Cache the dependencies
WORKDIR /app
COPY go.mod go.sum /app/
RUN go mod download

# Compile the application
COPY . /app
RUN --mount=type=cache,target=/root/.cache/go-build ./scripts/build.sh

# Step 2: build the image to be actually run
FROM scratch
USER 10001:10001
COPY --from=builder /app/bin/cnpg-i-sidecar-injector /app/bin/cnpg-i-sidecar-injector
ENTRYPOINT ["/app/bin/cnpg-i-sidecar-injector"]
