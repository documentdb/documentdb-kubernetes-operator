FROM golang:1.26.8-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/live-telemetry ./cmd/live-telemetry

FROM scratch
COPY --from=build /out/live-telemetry /live-telemetry
USER 65532:65532
ENTRYPOINT ["/live-telemetry"]
