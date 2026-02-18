# BCDR Testing Playground

This playground focuses on exercising DocumentDB Business Continuity and
Disaster Recovery (BCDR) scenarios. It builds on the AKS Fleet Deployment
playground and adds Chaos Mesh fault injection and a continuous insert/read
workload to measure downtime, data loss, and recovery behavior under HA and
regional failure conditions.

## Contents

- `deploy.sh`: Deploys a three-region AKS fleet (westus3, uksouth, eastus2),
  installs cert-manager, the DocumentDB operator, a multi-region DocumentDB
  cluster, and Chaos Mesh on every member cluster.
- `failure_insert_test.py`: Python script that continuously inserts documents
  into the primary and reads from two replicas, tracking insert counts, read
  counts, and downtime.
- `run_ha_failure_test.sh`: Runs the failure test and kills a primary cluster
  pod to test HA failover time. (uses `ha-failure.yaml`)
- `run_regional_failure_test.sh`: Runs the failure test and kills all the pods
  on the primary cluster, then initiates a regional failover (uses regional-failure.yaml)

## Prerequisites

- Azure CLI (`az`), `kubectl`, `jq`, `helm`
- Python 3 with packages from `requirements.txt` (`pip install -r requirements.txt`)
- An Azure subscription with permissions to create AKS clusters and DNS zones

## Reading results

Results will be of the form

```text
Final read count (read_host_1): 100
Final read count (read_host_2): 100
Data loss (read_host_1): 0
Data loss (read_host_2): 0
Downtime (s): 11.08
HA Primary Pod changed from documentdb-preview-bb8b4c62e10c285b-1 to documentdb-preview-bb8b4c62e10c285b-2
```

The issues you'll want to note are downtimes that are too long:

* More than 1 minute for HA
* More than 5 for Regional

Any data loss. Ideally the count for missing inserts will always be zero.

## Environment Variables

| Variable | Default | Description |
| --- | --- | --- |
| `RESOURCE_GROUP` | `documentdb-bcdr-test-rg` | Azure resource group for the fleet |
| `DOCUMENTDB_NAME` | `documentdb-preview` | Name of the DocumentDB custom resource |
| `DOCUMENTDB_NAMESPACE` | `documentdb-preview-ns` | Kubernetes namespace for DocumentDB |
| `TOTAL_DURATION_SECONDS` | `60` (HA) / `360` (regional) | How long the insert workload runs |
| `CHAOS_DELAY_SECONDS` | `15` (HA) / `30` (regional) | Seconds to wait before injecting chaos |
| `FAILOVER_DELAY_SECONDS` | `10` | REGIONAL ONLY Seconds to wait before performing regional failover |
| `USE_DNS_ENDPOINTS` | `false` | Use DNS SRV records instead of LoadBalancer IPs |
| `DNS_ZONE_FQDN` | *(empty)* | Azure DNS zone FQDN (required when `USE_DNS_ENDPOINTS=true`) |
| `HUB_REGION` | `westus3` | Hub region used for fleet management |
| `PRIMARY_CONTEXT` | *(auto-detected)* | Override the kubectl context of the primary cluster |
