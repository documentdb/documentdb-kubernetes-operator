# Long Haul Tests

Long haul tests validate that DocumentDB Kubernetes Operator clusters remain healthy under
continuous load over extended periods. They run a canary workload that writes and reads data,
performs management operations, and checks for data integrity.

> **Status:** Phase 1a (skeleton). The canary workload and management operations will be added
> in subsequent phases. See [design document](../../docs/designs/long-haul-test-design.md)
> for the full plan.

## Project Structure

```
test/longhaul/
├── go.mod                # Separate Go module (imports test/utils when available)
├── README.md             # This file
├── suite_test.go         # Ginkgo suite entry point (the canary)
├── longhaul_test.go      # BeforeSuite + long-running test specs
└── config/
    ├── config.go          # Config struct, env var loading, validation
    ├── suite_test.go      # Config unit test suite entry
    └── config_test.go     # Config unit tests
```

- **`test/longhaul/`** — The actual long-running canary. Designed to run for hours/days.
- **`test/longhaul/config/`** — Config parsing and validation. Fast unit tests, safe for CI.

## Quick Start

### Prerequisites

- A running Kubernetes cluster with DocumentDB deployed
- `kubectl` configured to access the cluster
- Go 1.25+

### Run the Config Unit Tests

These are fast and require no cluster:

```bash
cd test/longhaul
go test ./config/ -v
```

### Run the Long Haul Canary Locally

Against a local Kind cluster (see [development environment guide](../../docs/developer-guides/development-environment.md)):

```bash
cd test/longhaul

LONGHAUL_ENABLED=true \
LONGHAUL_CLUSTER_NAME=documentdb-sample \
LONGHAUL_NAMESPACE=default \
LONGHAUL_MAX_DURATION=10m \
go test ./... -v -timeout 0
```

> **Note:** Use `-timeout 0` to disable Go's default 10-minute test timeout for long runs.

### Build a Standalone Binary

For containerized deployment (Phase 2+):

```bash
cd test/longhaul
go test -c -o longhaul.test ./

# Run the compiled binary
LONGHAUL_ENABLED=true \
LONGHAUL_CLUSTER_NAME=documentdb-sample \
LONGHAUL_NAMESPACE=default \
./longhaul.test -test.v -test.timeout 0
```

## Configuration

All configuration is via environment variables. Tests are **gated** behind `LONGHAUL_ENABLED` —
they are safely skipped in regular CI runs (`go test ./...`).

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `LONGHAUL_ENABLED` | Yes | — | Must be `true`, `1`, or `yes` to run. Otherwise all tests skip. |
| `LONGHAUL_CLUSTER_NAME` | Yes | — | Name of the target DocumentDB cluster CR. |
| `LONGHAUL_NAMESPACE` | No | `default` | Kubernetes namespace of the target cluster. |
| `LONGHAUL_MAX_DURATION` | No | `30m` | Max test duration. Use `0s` for run-until-failure. |

> Additional configuration (writer count, operation cooldown, etc.) will be added in later phases
> as the corresponding features are implemented.

## CI Safety

The long haul tests are gated behind `LONGHAUL_ENABLED`. No CI workflow currently sets this
variable — do not add it to any PR-gated workflow.

1. `LONGHAUL_ENABLED` is not set in any CI workflow
2. The `BeforeSuite` calls `Skip()` when disabled
3. CI output shows `Suite skipped in BeforeSuite -- 0 Passed | 0 Failed | 1 Skipped`

> **Note:** For persistent canary deployment, the Job manifest explicitly sets
> `LONGHAUL_MAX_DURATION=0s` to enable run-until-failure mode. The default 30m timeout
> is only a safety net for local development.

The config unit tests (`test/longhaul/config/`) run unconditionally and are included in normal
CI test runs — they are fast (~0.002s) and require no cluster.
