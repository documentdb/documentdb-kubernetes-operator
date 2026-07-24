# Bash Scripts for Arc + Fleet Setup

These scripts automate the setup of DocumentDB on Azure Fleet Manager with Arc-enabled clusters.

## Prerequisites

- Azure CLI 2.50.0+ with `fleet`, `connectedk8s` extensions
- kubectl, Helm, Kind, Docker
- Logged into Azure: `az login`

## Usage

Run in order:

```bash
# 1. Create Fleet hub + AKS cluster (~10 min)
./setup-fleet-hub.sh

# 2. Create Kind cluster + Arc-enable (~5 min)
./setup-arc-member.sh

# 3. Deploy DocumentDB to all clusters (~5 min)
./deploy-documentdb-fleet.sh

# 4. Verify setup
./verify-portal.sh
```

## Environment Variables

All scripts support environment variable overrides:

| Variable | Default | Description |
|----------|---------|-------------|
| `RESOURCE_GROUP` | `documentdb-fleet-rg` | Azure resource group |
| `LOCATION` | `eastus` | Azure region |
| `FLEET_NAME` | `documentdb-fleet` | Fleet hub name |
| `AKS_CLUSTER` | `documentdb-aks` | AKS cluster name |
| `ARC_CLUSTER` | `documentdb-onprem` | Arc cluster name |

Example:
```bash
LOCATION=westus2 ./setup-fleet-hub.sh
```

## Help

Each script supports `-h` or `--help`:

```bash
./setup-fleet-hub.sh --help
```

## Cleanup

```bash
./cleanup.sh         # Interactive (asks for confirmation)
./cleanup.sh --force # Skip confirmation
```
