---
title: Metrics Reference
description: Reference for DocumentDB-owned metrics emitted by the operator-managed OpenTelemetry sidecar.
tags:
  - monitoring
  - metrics
  - opentelemetry
---

# Metrics Reference

This page documents metrics that are part of the DocumentDB operator monitoring contract. OpenTelemetry metric names are canonical; each backend may render names and labels differently.

## Available metrics

The sidecar in each DocumentDB pod collects three groups of metrics:

| Group | Source | Interval | Reported by |
|-------|--------|----------|-------------|
| [PostgreSQL instance](#postgresql-instance) | SQL queries against the local PostgreSQL server | 30 seconds | Every instance |
| [DocumentDB catalog](#documentdb-catalog) | SQL queries against the DocumentDB catalog | 5 minutes | Primary only |
| [Pod filesystem and network](#pod-filesystem-and-network) | OpenTelemetry Collector `hostmetrics` receiver | 30 seconds | Every instance |

Every metric carries these resource attributes:

| Attribute | Description |
|-----------|-------------|
| `documentdb.cluster` | DocumentDB cluster name |
| `k8s.namespace.name` | Kubernetes namespace |
| `k8s.pod.name` | Kubernetes pod name |

The Prometheus exporter turns attributes into labels, replaces dots with underscores, and adds unit and type suffixes to metric names. The tables below list both forms. For example:

```promql
documentdb_postgres_up{documentdb_cluster="my-cluster"}
```

### PostgreSQL instance

Each instance reports these metrics for its local PostgreSQL server. Use `k8s.pod.name` to tell instances apart.

| OpenTelemetry metric | Prometheus name | Type | Unit | Description |
|----------------------|-----------------|------|------|-------------|
| `documentdb.postgres.up` | `documentdb_postgres_up` | Gauge | | `1` when the local PostgreSQL container responds to the sidecar health query. |
| `documentdb.postgres.uptime` | `documentdb_postgres_uptime_seconds` | Gauge | `s` | Time since the local PostgreSQL server started. Resets whenever PostgreSQL restarts, including restarts inside a running pod. |
| `documentdb.postgres.replication.lag` | `documentdb_postgres_replication_lag_seconds` | Gauge | `s` | Standbys only. Time since the last replayed transaction, or `0` when the standby has replayed all the WAL it received. |
| `documentdb.postgres.replication.receiver.up` | `documentdb_postgres_replication_receiver_up` | Gauge | | Standbys only. `1` while the WAL receiver process runs, otherwise `0`. |
| `documentdb.postgres.blocks.hit` | `documentdb_postgres_blocks_hit_total` | Counter | `{block}` | Blocks found in PostgreSQL shared buffers. |
| `documentdb.postgres.blocks.read` | `documentdb_postgres_blocks_read_total` | Counter | `{block}` | Blocks read from outside shared buffers, including reads that the OS page cache serves. |

- A standby that loses its connection to the primary also reports a lag of `0` once it has replayed everything it received. Read the lag together with `documentdb.postgres.replication.receiver.up`.
- The primary does not report the replication metrics. After a standby is promoted, the Prometheus exporter keeps serving its last replication values for up to 6 minutes.
- The block counters cover the PostgreSQL database that stores DocumentDB data. Compute the shared-buffer hit ratio from their rates.

```promql
# Shared-buffer hit ratio per instance
rate(documentdb_postgres_blocks_hit_total[5m])
  / (rate(documentdb_postgres_blocks_hit_total[5m]) + rate(documentdb_postgres_blocks_read_total[5m]))

# Standbys whose WAL receiver is not running
documentdb_postgres_replication_receiver_up == 0
```

### DocumentDB catalog

The primary reports these metrics; standbys report none. They describe user data only: the `admin`, `config`, and `local` databases and collections whose names start with `system.` are excluded.

| OpenTelemetry metric | Prometheus name | Type | Unit | Description |
|----------------------|-----------------|------|------|-------------|
| `documentdb.database.count` | `documentdb_database_count` | Gauge | `{database}` | Number of user databases. |
| `documentdb.database.collection.count` | `documentdb_database_collection_count` | Gauge | `{collection}` | Number of collections in the database, not counting views. |
| `documentdb.database.view.count` | `documentdb_database_view_count` | Gauge | `{view}` | Number of views in the database. |
| `documentdb.database.index.count` | `documentdb_database_index_count` | Gauge | `{index}` | Number of indexes in the database whose build has completed, including `_id` indexes. |
| `documentdb.database.storage.size` | `documentdb_database_storage_size_bytes` | Gauge | `By` | On-disk size of the database's collections, excluding indexes. Matches `storageSize` from the `dbStats` command. |

- Every catalog metric except `documentdb.database.count` has a `db.namespace` attribute (`db_namespace` in Prometheus) that holds the database name. Database names leave the cluster with your metrics, and each database adds four series.
- After a switchover or failover, the new primary reports catalog metrics at its next collection, within 5 minutes. The Prometheus exporter on the former primary keeps serving its last values for up to 6 minutes, so aggregate across pods with `max` rather than `sum`.
- With OTLP push, the backend receives one data point per series every 5 minutes. In backends with a 5 minute lookback window, such as Prometheus, query over a longer range.

```promql
# Storage size per database
max by (k8s_namespace_name, documentdb_cluster, db_namespace) (documentdb_database_storage_size_bytes)

# The same query for series ingested over OTLP
max by (k8s_namespace_name, documentdb_cluster, db_namespace) (last_over_time(documentdb_database_storage_size_bytes[10m]))
```

### Pod filesystem and network

The sidecar reads these metrics from inside its own pod with the OpenTelemetry Collector `hostmetrics` receiver. It needs no node-level collector or kubelet access. A node-level collector that runs the same receiver reports the same `system.*` names; the sidecar's series are the ones with the `documentdb.cluster` attribute.

The `system.*` names and attributes come from OpenTelemetry Collector Contrib 0.149.0, the operator's default collector version. Upstream marks these metrics as Development stability, so other collector versions can rename them or change their attributes.

#### Data volume

The operator mounts the PostgreSQL data volume into the sidecar read-only, on an empty subdirectory, at `/var/lib/otel/pgdata`. The sidecar sees the capacity and usage of the whole volume but none of its data files.

| OpenTelemetry metric | Prometheus name | Type | Unit | Description |
|----------------------|-----------------|------|------|-------------|
| `system.filesystem.usage` | `system_filesystem_usage_bytes` | UpDownCounter | `By` | Data volume space by `state`: `used`, `free`, or `reserved`. `free` is the space available to PostgreSQL, as in the `Avail` column of `df`. |
| `system.filesystem.inodes.usage` | `system_filesystem_inodes_usage` | UpDownCounter | `{inodes}` | Data volume inodes by `state`: `used` or `free`. |
| `system.filesystem.utilization` | `system_filesystem_utilization_ratio` | Gauge | `1` | Fraction of the data volume in use, from `0` to `1`, as in the `Use%` column of `df`. |

These series also carry `device`, `mode`, `mountpoint`, and `type` attributes. `mountpoint` is always `/var/lib/otel/pgdata`, and `mode` is `ro` because the sidecar's mount is read-only; PostgreSQL's own mount stays writable.

#### Network

The sidecar shares the pod's network namespace, so these metrics count all pod traffic on each non-loopback interface (the `device` attribute, such as `eth0`): client traffic to the gateway, replication between instances, and metric export. Loopback traffic between the gateway, PostgreSQL, and the sidecar is excluded.

| OpenTelemetry metric | Prometheus name | Type | Unit | Description |
|----------------------|-----------------|------|------|-------------|
| `system.network.io` | `system_network_io_bytes_total` | Counter | `By` | Bytes by `direction`: `receive` or `transmit`. |
| `system.network.packets` | `system_network_packets_total` | Counter | `{packets}` | Packets by `direction`. |
| `system.network.errors` | `system_network_errors_total` | Counter | `{errors}` | Errors by `direction`. |
| `system.network.dropped` | `system_network_dropped_total` | Counter | `{packets}` | Dropped packets by `direction`. |

Each pod reports 6 filesystem series, plus 8 network series per non-loopback interface.

```promql
# Free space on the data volume
system_filesystem_usage_bytes{documentdb_cluster="my-cluster", state="free"}

# Pod network throughput in bytes per second
rate(system_network_io_bytes_total{documentdb_cluster="my-cluster"}[5m])
```

### Monitoring role permissions

The sidecar connects to PostgreSQL as the `otel_monitor` role. Once the primary is healthy, the operator grants this role `USAGE` on the `documentdb_core`, `documentdb_api_internal`, and `documentdb_api_catalog` schemas. DocumentDB checks the first two for any query that calls a function or operator, and the catalog metrics read the third. The role has no access to the `documentdb_data` schema that stores documents.

Until the grants apply, the sidecar reports only `documentdb.postgres.up` and the filesystem and network metrics. When you disable monitoring, the operator revokes the grants before it removes the role.

## Planned DocumentDB metric groups

The preview monitoring API is intentionally small while instrumentation lands. These areas are planned or out of scope for the current preview docs:

| Area | Status |
|------|--------|
| Gateway application metrics | Planned. The sidecar can receive local OTLP from the gateway, but user-facing gateway metrics will be documented after a public gateway image emits them. |
| CNPG/PostgreSQL internals | The sidecar reports the curated PostgreSQL metrics listed above. Other CloudNativePG and PostgreSQL internals, such as WAL health, are out of preview scope. |
| Operator controller metrics | Not yet exposed end-to-end through the operator Helm chart. |

## Gateway probe connections

The `documentdb-gateway` container has TCP startup and liveness probes on port 10260 (see [Gateway Container Restarts](../high-availability/local-ha.md#gateway-container-restarts)). Each probe opens a connection and closes it without sending data. The liveness probe runs every 10 seconds, so each pod's gateway accepts about six such connections per minute and logs each one as a failed connection at `ERROR` level. Expect this baseline in gateway logs, and in gateway connection metrics if your gateway image emits them.

## Pod and container resource metrics

The sidecar reports the data volume usage and pod network I/O listed under [Pod filesystem and network](#pod-filesystem-and-network). CPU, memory, ephemeral storage, and node metrics are Kubernetes platform metrics. The DocumentDB operator does not install a node-level collector or grant kubelet permissions to DocumentDB pods.

Use your existing platform collector, managed cloud agent, kube-prometheus-stack, or OTel Collector DaemonSet for these metrics. Filter that data to DocumentDB workloads using pod or container attributes such as:

| Attribute | Typical values |
|-----------|----------------|
| `k8s.namespace.name` | Namespace that contains the DocumentDB cluster |
| `k8s.pod.name` | DocumentDB pod name |
| `k8s.container.name` | `postgres`, `documentdb-gateway`, `otel-collector` |

If your cluster does not already collect kubelet-backed resource metrics, the playground includes a reference DaemonSet at [`documentdb-playground/telemetry/container-metrics/`](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/telemetry/container-metrics). Treat it as a starting point for demo or platform-owned deployments, not as part of the DocumentDB operator monitoring contract.
