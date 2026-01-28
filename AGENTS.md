# AGENTS.md - AI Coding Assistant Briefing

This document serves as a comprehensive briefing for AI coding assistants working with the DocumentDB Kubernetes Operator repository. It covers the WHAT, WHY, and HOW of contributing to this codebase.

---

## Architecture Overview

The DocumentDB Kubernetes Operator is a Kubernetes operator that manages DocumentDB (MongoDB-compatible) clusters. The project extends Kubernetes with Custom Resource Definitions (CRDs) to enable declarative management of DocumentDB deployments.

**Core Components:**

- **documentdb-operator** (in `operator/src/`)
  - Manages `DocumentDB`, `Backup`, and `ScheduledBackup` CRDs
  - Reconciles desired state with actual cluster state
  - Handles cluster lifecycle, replication, and high availability
  - Built on [CloudNative-PG](https://cloudnative-pg.io/) for robust PostgreSQL foundation

- **kubectl-documentdb plugin** (in `documentdb-kubectl-plugin/`)
  - CLI plugin for managing DocumentDB resources
  - Provides commands for status, health checks, and operations

- **Helm Chart** (in `operator/documentdb-helm-chart/`)
  - Packages operator deployment configuration
  - Manages CRDs and RBAC resources
  - Integrates CloudNative-PG as a dependency

---

## Tech Stack

**Languages:**
- **Go:** Go 1.25.0 (check `go.mod` for exact version)
- **Runtime Platform:** Linux containers (multi-arch: amd64, arm64, s390x, ppc64le)
- **Developer Platform:** Linux, macOS, WSL2 (devcontainer recommended)

**Core Frameworks:**
- **Kubernetes:** client-go, api, apimachinery
- **controller-runtime:** Kubebuilder-based operator framework
- **CloudNative-PG:** PostgreSQL operator foundation
- **cert-manager:** TLS certificate management

**Key Dependencies:**
- **cloudnative-pg/cloudnative-pg:** Core database operator
- **cloudnative-pg/machinery:** Shared utilities
- **kubernetes-csi/external-snapshotter:** Volume snapshot support for backups
- **Ginkgo/Gomega:** BDD testing framework
- **Helm 3:** Deployment and packaging

**Container Base:**
- Distroless or minimal base images
- Non-root user execution

**Tools:**
- controller-gen, golangci-lint, kind, kustomize, setup-envtest, helm

### Version Compatibility Matrix

| Component | Version | Notes |
|-----------|---------|-------|
| Kubernetes | 1.30+ | Based on k8s.io/api v0.34.2 |
| CloudNative-PG | 1.28.0 | Helm chart uses CNPG chart 0.26.1 |
| cert-manager | 1.19.2 | Required for TLS certificate management |
| controller-runtime | 0.22.4 | Kubebuilder framework |
| Go | 1.25.0 | See `operator/src/go.mod` |
| Helm | 3.x | Required for deployment |

> **Note:** When the project moves to GA, maintain a separate version compatibility matrix for each release to track dependency versions across operator releases.

---

## Build & Test Commands

All commands should be run from the `operator/src/` directory unless otherwise specified. For comprehensive setup instructions, see the [Development Environment Guide](docs/developer-guides/development-environment.md).

### Build

```bash
# Build operator binary
make build

# Build Docker image
make docker-build

# Build and push multi-arch images
make docker-buildx

# Build kubectl plugin
make build-kubectl-plugin

# Generate installer manifests
make build-installer
```

### Test

```bash
# Run unit tests (uses ENVTEST)
make test

# Run e2e tests (requires Kind cluster)
make test-e2e

# Run tests from repository root
go test ./...
```

### Linting & Verification

```bash
# Run golangci-lint
make lint

# Run lint and fix issues
make lint-fix

# Format code
make fmt

# Run go vet
make vet
```

### Code Generation

```bash
# Generate CRDs and RBAC manifests
make manifests

# Generate DeepCopy methods
make generate

# Both manifests and code generation
make manifests generate
```

### Local Development

```bash
# Using devcontainer (recommended):
# 1. Open in VS Code
# 2. "Reopen in Container"

# Using deploy script (creates kind cluster and deploys):
# Run from operator/src/ directory:
DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh

# Run operator locally against cluster
make run

# View operator logs
stern documentdb-operator -n documentdb-operator
```

---

## Conventions & Patterns

### Folder Structure

```
/
├── operator/
│   ├── src/                           # Operator source code
│   │   ├── api/preview/               # CRD type definitions
│   │   ├── cmd/                       # Main entry point
│   │   ├── internal/
│   │   │   ├── controller/            # Reconciliation controllers
│   │   │   ├── cnpg/                  # CloudNative-PG integration
│   │   │   └── utils/                 # Shared utilities
│   │   └── config/                    # Kustomize manifests
│   ├── documentdb-helm-chart/         # Helm chart
│   │   ├── Chart.yaml
│   │   ├── values.yaml
│   │   ├── crds/                      # Generated CRDs
│   │   └── templates/                 # Helm templates
│   └── cnpg-plugins/                  # CNPG sidecar plugins
├── documentdb-kubectl-plugin/         # kubectl plugin
├── documentdb-playground/             # Deployment examples & cloud setups
│   ├── aks-setup/                     # Azure AKS deployment automation
│   ├── aws-setup/                     # AWS EKS deployment automation
│   ├── aks-fleet-deployment/          # Multi-region AKS with KubeFleet
│   ├── multi-cloud-deployment/        # Cross-cloud HA (AKS + GKE + EKS)
│   ├── tls/                           # TLS configuration (self-signed, provided, cert-manager)
│   ├── telemetry/                     # OpenTelemetry, Prometheus, Grafana setup
│   └── operator-upgrade-guide/        # Operator upgrade testing
├── docs/
│   ├── developer-guides/              # Development documentation
│   ├── designs/                       # Design documents
│   └── operator-public-documentation/ # User documentation
├── .github/
│   ├── workflows/                     # CI/CD pipelines
│   ├── agents/                        # AI agent configurations
│   └── copilot-instructions.md        # Copilot instructions
├── AGENTS.md                          # This file
├── CHANGELOG.md                       # Version history
├── CONTRIBUTING.md                    # Contribution guidelines
└── README.md                          # Project overview
```

### Naming Conventions

- **Controllers:** `{resource}_controller.go`
- **Tests:** `{name}_test.go`
- **Internal packages:** lowercase, no underscores
- **Generated files:** `zz_generated.*.go`
- **CRDs:** `{group}.{domain}_{resources}.yaml`

### Core APIs

- **Primary CRDs:**
  - `DocumentDB` - Main cluster resource definition
  - `Backup` - Point-in-time backup configuration
  - `ScheduledBackup` - Scheduled backup definitions
- **API Group:** `documentdb.io`
- **API Version:** `preview` (currently in preview)

### Controller Patterns

- Reconciliation logic must be **idempotent**
- Update status conditions appropriately using standard condition types
- Emit events for significant state changes
- Use finalizers for cleanup operations
- Follow controller-runtime best practices

---

## Git Workflows

### Branching Strategy

- **Main branch:** `main` (default, protected)
- **Feature branches:** `developer/feature-name` pattern, created from `main`, merged via PR

### Commit Message Format

Follow conventional commits:
```
<type>: <description>

<detailed description>
```

Types:
- `feat:` new features
- `fix:` bug fixes
- `docs:` documentation changes
- `test:` test additions/changes
- `refactor:` code refactoring
- `chore:` maintenance tasks

### PR Requirements

- Must pass all CI checks (build, test, lint)
- Should pass local tests (deploy to Kind and run test suite)
- All issues found by the code review agent are fixed/addressed
- CLA signature required (automatic via CLA bot)
- Reasonable title and description
- Tests for new functionality
- Documentation updates as needed

### CI Workflows

- `test-build-and-package.yml` - Build and package validation
- `test-integration.yml` - Integration tests
- `test-E2E.yml` - End-to-end tests
- `test-backup-and-restore.yml` - Backup feature tests
- `build_images.yml` - Docker image builds
- `release_images.yml` - Release automation
- `deploy_docs.yml` - Documentation deployment

---

## Boundaries (What Not to Touch)

### Generated Files (require special process)

**Schema/CRD Files:**
- `/operator/src/api/preview/zz_generated.deepcopy.go` - Generated by controller-gen
- `/operator/documentdb-helm-chart/crds/*.yaml` - Generated CRDs
- **Process:** Modify types in `/operator/src/api/preview/*_types.go`, then run `make manifests generate`

**Generated Manifests:**
- Files under `/operator/src/config/crd/bases/`
- **Process:** Run `make manifests` after API changes

### CI/CD & Project Metadata

**Modify with caution (consult team):**
- `/.github/workflows/*.yaml` - CI pipelines
- `/operator/src/Makefile` - Core build logic
- `/operator/src/go.mod` - Dependencies (use `go mod tidy`)
- `/operator/src/PROJECT` - Kubebuilder project config

### Helm Charts (requires careful review)

- `/operator/documentdb-helm-chart/Chart.yaml` - Chart metadata
- `/operator/documentdb-helm-chart/values.yaml` - Default values
- `/operator/documentdb-helm-chart/templates/*.yaml` - Chart templates

### Security & Compliance

- `/LICENSE` - MIT license
- `/CODEOWNERS` - Code ownership
- `/SECURITY.md` - Security policy
- `/contribute/developer-certificate-of-origin` - DCO

---

## Important Notes for AI Agents

1. **Never commit to `main` directly** - always use PRs
2. **Never commit secrets or passwords** - use Kubernetes Secrets or environment variables
3. **CRD changes** require running `make manifests generate` and may break compatibility
4. **API changes** in `/operator/src/api/preview/` trigger CRD regeneration
5. **Generated files** must be committed after running generators
6. **Helm chart changes** may require updating CRDs in `/operator/documentdb-helm-chart/crds/`
7. **Go version changes** should align with `go.mod` specification
8. **Dependencies:** Avoid unnecessary additions; security and maintenance matter

### Development Workflow

1. Make changes to source code
2. Run `make manifests generate` if API types changed
3. Run `make fmt vet lint`
4. Run `make test`
5. Commit both source and generated files
6. CI will verify everything is in sync

### Key Components to Understand

**operator (operator/src/):**
- `DocumentDB` controller - manages cluster lifecycle
- `Backup` controller - handles backup operations
- `ScheduledBackup` controller - manages scheduled backups
- `Certificate` controller - TLS certificate management
- CloudNative-PG integration layer

**kubectl plugin (documentdb-kubectl-plugin/):**
- Status command - cluster status information
- Health command - health checks
- Events command - event history
- Promote command - replica promotion

### Testing Guidelines

- Use Ginkgo/Gomega for BDD-style tests
- Place unit tests alongside source files (`*_test.go`)
- E2E tests run via `make test-e2e` (requires Kind cluster)
- Mock external dependencies appropriately
- Ensure tests are idempotent and isolated

### Code Review

For thorough code reviews, reference the code review agent:
- See `.github/agents/code-review-agent.md` for review criteria
- See `.github/copilot-instructions.md` for coding standards

---

## Deployment Examples (documentdb-playground/)

The `documentdb-playground/` directory contains production-ready deployment examples and automation scripts for various cloud environments. AI agents should reference these when helping users deploy DocumentDB.

### Cloud Platform Setups

| Directory | Description | Key Features |
|-----------|-------------|--------------|
| `aks-setup/` | Azure Kubernetes Service | AKS cluster automation, Azure CNI, cert-manager, Azure LoadBalancer |
| `aws-setup/` | Amazon EKS | EKS cluster automation, NLB configuration, cross-zone load balancing |
| `aks-fleet-deployment/` | Multi-region Azure | KubeFleet orchestration, VNet peering, multi-region HA |
| `multi-cloud-deployment/` | Cross-cloud HA | AKS + GKE + EKS, Istio service mesh, cross-cloud replication |

### Configuration Examples

| Directory | Description | Key Features |
|-----------|-------------|--------------|
| `tls/` | TLS Certificate Setup | Self-signed, provided (Key Vault), cert-manager modes |
| `telemetry/` | Observability Stack | OpenTelemetry, Prometheus, Grafana, multi-tenant monitoring |
| `operator-upgrade-guide/` | Upgrade Testing | Local dev workflow for testing operator upgrades |

### When to Reference Playground

- **User asks about cloud deployment**: Point to appropriate `*-setup/` directory
- **Multi-region/HA questions**: Reference `aks-fleet-deployment/` or `multi-cloud-deployment/`
- **TLS/certificate questions**: Reference `tls/` with its multiple modes
- **Monitoring/observability**: Reference `telemetry/` for OpenTelemetry setup
- **Upgrade procedures**: Reference `operator-upgrade-guide/`

---

## Additional Resources

- [Development Environment Guide](docs/developer-guides/development-environment.md)
- [Public Documentation](https://documentdb.io/documentdb-kubernetes-operator/preview/)
- [Contributing Guide](CONTRIBUTING.md)
- [Changelog](CHANGELOG.md)

---

**Last Updated:** 2026-01-22
