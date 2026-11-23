# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

# Build the manager binary
ARG TARGETOS
ARG TARGETARCH
ARG ARCH
FROM --platform=${TARGETOS:-linux}/${ARCH:-${TARGETARCH}} mcr.microsoft.com/oss/go/microsoft/golang:1.26.11-azurelinux3.0 AS builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY cmd/main.go cmd/main.go
COPY api/ api/
COPY internal/ internal/

# Build
# the GOARCH has not a default value to allow the binary be built according to the host where the command
# was called. For example, if we call make docker-build in a local env which has the Apple Silicon M1 SO
# the docker BUILDPLATFORM arg will be linux/arm64 when for Apple x86 it will be linux/amd64. Therefore,
# by leaving it empty we can ensure that the container and binary shipped on it will have the same platform.
# The ARCH build arg can be used to override the target architecture (e.g., amd64, arm64)
RUN GOEXPERIMENT=nosystemcrypto CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${ARCH:-${TARGETARCH}} go build -a -o manager cmd/main.go

# The manager is static, so the runtime image needs no shell, package manager
# or SSH, only CA certificates, tzdata and /tmp.
FROM --platform=${TARGETOS:-linux}/${ARCH:-${TARGETARCH}} mcr.microsoft.com/azurelinux/distroless/minimal:3.0
WORKDIR /
COPY --from=builder /workspace/manager .
# Same uid/gid as the old Alpine `manager` user; the chart pins runAsUser/runAsGroup to it.
USER 100:101

ENTRYPOINT ["/manager"]
