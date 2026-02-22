# DocumentDB Kubernetes Operator – Development Environment

This guide walks you through two supported workflows for contributing to the
DocumentDB Kubernetes Operator:

1. **VS Code devcontainer (recommended)** – consistent, pre-baked toolchain
   that mirrors CI and includes Docker-in-Docker, kind, kubectl/helm, Azure
   CLI, mongosh, golangci-lint, and other utilities.
2. **Native host setup** – install the required toolchain directly on your
   Linux, macOS, or WSL2 environment.

Whichever path you choose, the repository Makefile and helper scripts provide a
consistent developer experience. Start by cloning or forking the repository as
usual, then proceed with the sections below.

```bash
# From a shell on your workstation
git clone https://github.com/documentdb/documentdb-kubernetes-operator.git
cd documentdb-kubernetes-operator
```

---

## 1. Developing with the VS Code devcontainer

### Prerequisites

- [Docker Desktop](https://www.docker.com/products/docker-desktop/) or another
  Docker-compatible runtime with virtualization enabled.
- [Visual Studio Code](https://code.visualstudio.com/) with the
  [Dev Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers)
  extension.
- ~20 GB of free disk space for the base image layers and build artifacts.

### Launching the container

1. Open the repository folder in VS Code.
2. Follow the prompt **“Reopen in Container”**, or run the “Dev Containers:
   Reopen in Container” command from the command palette.
3. VS Code builds the container defined in
   `.devcontainer/devcontainer.json` using the
   `mcr.microsoft.com/devcontainers/go:1-1.23-bullseye` base image. The build
   installs the following features:
   - Docker-in-Docker runtime for building and running images
   - kind + kubectl/helm/kubectx/kubens/stern for Kubernetes workflows
   - Azure CLI and kubelogin for interacting with AKS and Fleet
   - golangci-lint for static analysis

4. After the container starts, the `postCreateCommand` configures helpful shell
   aliases (for example, `k` → `kubectl`, with tab completion) and downloads Go
   module dependencies.

### Daily workflow inside the devcontainer

All commands run inside the container root (`/workspaces/documentdb-kubernetes-operator`).
Common tasks:

| Task | Command |
| ---- | ------- |
| List available targets | `make help` |
| Regenerate CRDs and deepcopy code | `make manifests generate` |
| Lint and test | `make lint && make test` |
| Build the operator binary | `make build` |
| Build & push local Docker images (operator + sidecar) | `DEPLOY=true ./scripts/development/deploy.sh --build-only` |
| Spin up a kind cluster, push images, deploy operator & sample | `DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh` |
| View operator logs | `stern documentdb-operator -n documentdb-operator` |

> **Tip**: The devcontainer bind-mounts `/lib/modules` from the host to support
> kind’s IPVS mode. Ensure Docker Desktop (or your Linux Docker service)
> exposes this path if you modify the configuration.

### Useful scripts

- [`scripts/development/deploy.sh`](../../scripts/development/deploy.sh): wraps
  image build/push + kind bootstrap + Helm/Kustomize deployment. Set
  `DEPLOYMENT_METHOD` to `helm` (default) or `kustomize`.
- [`scripts/operator/install_operator.sh`](../../scripts/operator/install_operator.sh):
  packages the Helm chart and installs it into `documentdb-operator`
  namespace. Automatically removes any existing release before reinstalling.
- [`scripts/operator/uninstall_operator.sh`](../../scripts/operator/uninstall_operator.sh):
  cleans operator namespace and related CRDs.

---

## 2. Developing on your local machine (Linux, macOS, WSL2)

If you prefer not to use containers, install the same toolchain locally. The
commands below assume a Unix-like shell (bash/zsh). Windows users should follow
the Linux instructions inside WSL2.

### Required tools

| Component | Minimum version | Notes |
| --------- | --------------- | ----- |
| Go | 1.25.0 | Matches `go.mod`. Install via package manager or [go.dev](https://go.dev/dl) |
| Docker Engine | 24.x | Needed for image builds and kind |
| kind | 0.31+ | [kind.sigs.k8s.io](https://kind.sigs.k8s.io/) |
| kubectl | 1.30+ | Align with the Kubernetes version you test against |
| Helm | 3.15+ | Required by deployment scripts |
| golangci-lint | 1.55+ | `curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh \| sh -s -- -b /usr/local/bin` |
| jq, make, git, tar, gzip, sed, awk | latest | Usually part of the OS base image |
| Optional: Azure CLI, kubelogin, mongosh | latest | Needed for Azure Fleet testing and DocumentDB samples |

#### Linux (Debian/Ubuntu example)

```bash
sudo apt-get update
sudo apt-get install -y build-essential curl git jq make tar gzip
sudo snap install go --channel=1.25/stable
curl -Lo kind https://kind.sigs.k8s.io/dl/v0.31.0/kind-linux-amd64 && chmod +x kind && sudo mv kind /usr/local/bin/
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" && chmod +x kubectl && sudo mv kubectl /usr/local/bin/
curl https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b /usr/local/bin
```

#### macOS (Homebrew)

```bash
brew update
brew install go@1.25 docker kind kubectl helm jq make coreutils findutils
brew install golangci-lint
```

If you use Docker Desktop, enable the Kubernetes networking settings recommended
by the kind quick-start guide. On macOS, disable AirPlay (port 5000) if you run
local registries on that port.

### Optional tools

- **Azure CLI + kubelogin** (`brew install azure-cli kubelogin` or `apt-get install azure-cli`)
- **mongosh** for interacting with the sample DocumentDB cluster (`brew install mongosh` or `apt-get install mongodb-mongosh`)
- **stern** for multi-pod log streaming (`brew install stern`)

### Validating your setup

```bash
# Confirm Go toolchain
make help

# Run lint and unit tests
make lint
make test

# Spin up or refresh the kind environment (includes local registry)
DEPLOY=true DEPLOY_CLUSTER=true ./scripts/development/deploy.sh

# When finished
./scripts/development/kind_with_registry.sh delete
```

The `deploy.sh` script builds operator and sidecar images tagged with the
current Git SHA (configurable via `TAG`). It then pushes them into the local
registry (`localhost:5001` by default), primes the kind cluster, and deploys the
operator through Helm.

---

## 3. Repository layout & common workflows

| Directory / Script | Purpose |
| ------------------ | ------- |
| [`cmd/`](../../cmd/) | Operator entry point (main.go) |
| [`internal/`](../../internal/) | Controller logic, utilities, replication code |
| [`api/`](../../api/) | CRD types and generated DeepCopy files |
| [`documentdb-helm-chart/`](../../documentdb-helm-chart/) | Helm chart for operator deployment |
| [`scripts/development/`](../../scripts/development/) | Local dev tooling (kind setup, deployment helpers) |
| [`scripts/operator/`](../../scripts/operator/) | Helm install/uninstall automation |
| [`scripts/deployment-examples/`](../../scripts/deployment-examples/) | Sample manifests (including mongosh client) |

### Make targets you’ll use often

- `make manifests generate` – update CRDs and DeepCopy sources after editing API types
- `make fmt` – gofmt all Go sources
- `make lint` / `make lint-fix` – run golangci-lint (and autofix where possible)
- `make test` – execute unit tests via controller-runtime envtest
- `make build` – compile the manager binary (`bin/manager`)
- `make docker-build` – build operator container image (`controller`)
- `make docker-buildx` – multi-architecture build (requires Docker Buildx)
- `make build-installer` – produce `install.yaml` bundle used for alternative installs

### Cleaning up

- `make clean` – remove compiled binaries
- `./scripts/development/kind_with_registry.sh delete` – delete the dev kind cluster and local registry container
- `docker image prune` – remove dangling image layers after repeated builds

---

## 4. Contributing guidelines

Before opening a pull request, review the repository-wide
[CONTRIBUTING.md](../../CONTRIBUTING.md) for the CLA process, style guidance,
branching expectations, and code of conduct. Helpful reminders:

- Always run `make lint` and `make test` locally (or inside the devcontainer)
  before pushing.
- Include unit tests, e2e tests, or integration checks when modifying controller logic.
- Keep Helm chart changes in sync with generated manifests and sample values.
- Update documentation (including this guide) if you add new dependencies or workflows.

For questions or proposals, open an issue or start a discussion in the GitHub
repository. Happy hacking!
