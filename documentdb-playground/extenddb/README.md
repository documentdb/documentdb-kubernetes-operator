# ExtendDB (DynamoDB API) on DocumentDB

This playground runs [ExtendDB](https://extenddb.org) — an open-source,
DynamoDB-wire-protocol-compatible server — configured with its MongoDB
storage backend pointed at a DocumentDB instance in Kubernetes. Any AWS SDK,
CLI, or tool that speaks the DynamoDB API works against ExtendDB unchanged,
while the data actually lives in DocumentDB.

> **Disclaimer:** ExtendDB is an independent open source project; it is not
> Amazon DynamoDB and contains no DynamoDB source code. "DynamoDB" is a
> trademark of Amazon.com, Inc. This playground is not affiliated with or
> endorsed by AWS.

> [!NOTE]
> **Transaction compatibility:** DocumentDB does not support MongoDB's
> `snapshot` transaction read concern, so this playground configures
> `storage.mongodb.transaction_read_concern` as `majority`. ExtendDB keeps
> `snapshot` as the default for real MongoDB deployments. Using `majority`
> enables DocumentDB compatibility, but concurrent transactions do not get
> snapshot isolation and may observe a different view of data than they
> would with `snapshot`.

## Architecture

```
┌───────────────────────────────────────────────────────────┐
│                     Kubernetes Cluster                    │
│                                                            │
│   AWS SDK / CLI                                           │
│        │  DynamoDB wire protocol (HTTPS)                  │
│        ▼                                                  │
│  ┌──────────────┐   MongoDB wire protocol   ┌────────────┐│
│  │   ExtendDB   │──────────────────────────▶│ DocumentDB ││
│  │ (DynamoDB    │  connection_string with    │  (Gateway) ││
│  │  API server) │  replicaSet=rs0            └─────┬──────┘│
│  └──────────────┘                                  │       │
│                                                     ▼       │
│                                              ┌────────────┐│
│                                              │ PostgreSQL ││
│                                              │   (CNPG)   ││
│                                              └────────────┘│
└───────────────────────────────────────────────────────────┘
```

ExtendDB's storage layer is trait-based and backend-agnostic; the MongoDB
backend implementation issues standard MongoDB CRUD/transaction operations,
which DocumentDB's gateway serves against its Postgres-backed engine.

## Prerequisites

- A Kubernetes cluster (kind, AKS, EKS, etc.) with the
  [DocumentDB operator](../../README.md) installed
- `kubectl`, `docker`, and `envsubst` (part of `gettext`) installed locally
- AWS CLI v2, for the connectivity smoke test
- No official ExtendDB container image ships with the MongoDB backend
  enabled (only a PostgreSQL-backend image is published upstream), so this
  playground builds one from source — a Rust toolchain is **not** required
  locally; the build happens inside Docker (see [`Dockerfile`](Dockerfile))

## Quick Start

```bash
# 1. Deploy a DocumentDB instance (skip if you already have one)
#    Edit documentdb.yaml first and replace the placeholder password.
kubectl create namespace documentdb-test
kubectl apply -f documentdb.yaml
kubectl wait --for=jsonpath='{.status.status}'="Cluster in healthy state" \
    documentdb/documentdb-cluster -n documentdb-test --timeout=300s

# 2. Build the ExtendDB (MongoDB backend) image and load it into your cluster
./scripts/build-image.sh

# 3. Deploy ExtendDB, wired up to the DocumentDB instance
./scripts/deploy.sh
# Save the printed admin username/password -- shown once, cannot be
# retrieved later. NOTE: these are management-API credentials, not a SigV4
# access key/secret pair -- see "Creating a DynamoDB API access key" below
# for the one-time step to turn them into one.

# 4. Create an account, IAM user, policy, and access key for DynamoDB calls
#    (see "Creating a DynamoDB API access key" below for the full walkthrough)

# 5. Test the DynamoDB API end to end
EXTENDDB_ACCESS_KEY_ID=<access key from step 4> \
EXTENDDB_SECRET_ACCESS_KEY=<secret key from step 4> \
    ./scripts/test-connection.sh

# 6. (Optional) Run the small Python demo, or explore interactively with ddbsh
kubectl port-forward svc/extenddb 18443:18443 -n extenddb &
pip install -r demo/requirements.txt
EXTENDDB_ACCESS_KEY_ID=<from step 3> \
EXTENDDB_SECRET_ACCESS_KEY=<from step 3> \
    ./demo/demo.py

# Cleanup when done
./scripts/cleanup.sh
```

`scripts/deploy.sh` defaults to `DOCUMENTDB_NAMESPACE=documentdb-test`,
`DOCUMENTDB_CLUSTER=documentdb-cluster`, and `EXTENDDB_NAMESPACE=extenddb`.
Override via env vars if your cluster uses different names:

```bash
DOCUMENTDB_NAMESPACE=my-ns DOCUMENTDB_CLUSTER=my-cluster EXTENDDB_NAMESPACE=dynamo \
    ./scripts/deploy.sh
```

## Creating a DynamoDB API access key

The username/password `extenddb init` prints are for the **management API**
(`extenddb manage`) only — they authenticate account/user/policy
administration, not DynamoDB requests, which use AWS SigV4 access
key/secret pairs instead. Create an account, an IAM user, a policy granting
DynamoDB access, and an access key with `extenddb manage` (run inside the
running pod, which already has the binary and config):

```bash
ADMIN_USER=admin
ADMIN_PASS=<password from 'extenddb init', printed by scripts/deploy.sh>

# Create an account
kubectl exec -n extenddb deploy/extenddb -- extenddb manage \
    --user "$ADMIN_USER" --password "$ADMIN_PASS" \
    --config /var/lib/extenddb/extenddb.toml \
    create-account --account-name playground-demo
# -> note the returned account_id, e.g. 676578798544

ACCOUNT_ID=<account_id from above>

# Create an IAM user with a console password
kubectl exec -n extenddb deploy/extenddb -- extenddb manage \
    --user "$ADMIN_USER" --password "$ADMIN_PASS" \
    --config /var/lib/extenddb/extenddb.toml \
    create-user --account-id "$ACCOUNT_ID" \
    --user-name demo-user --user-password <choose-a-password>

# Attach a policy granting full DynamoDB access
kubectl exec -n extenddb deploy/extenddb -- extenddb manage \
    --user "$ADMIN_USER" --password "$ADMIN_PASS" \
    --config /var/lib/extenddb/extenddb.toml \
    put-user-policy --account-id "$ACCOUNT_ID" --user-name demo-user \
    --policy-name FullAccess \
    --policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"dynamodb:*","Resource":"*"}]}'

# Create the access key -- shown once, save it now
kubectl exec -n extenddb deploy/extenddb -- extenddb manage \
    --user "$ACCOUNT_ID/demo-user" --password <the-password-you-chose> \
    --config /var/lib/extenddb/extenddb.toml \
    create-access-key
```

Use the returned `access_key_id`/`secret_access_key` as
`EXTENDDB_ACCESS_KEY_ID`/`EXTENDDB_SECRET_ACCESS_KEY` for
`scripts/test-connection.sh`, or as `AWS_ACCESS_KEY_ID`/
`AWS_SECRET_ACCESS_KEY` for any AWS SDK/CLI call against the ExtendDB
endpoint. See [`docs/getting-started.md` §7](https://github.com/ExtendDB/extenddb/blob/main/docs/getting-started.md)
upstream for the full IAM user/group/role/policy management surface.

## What the Scripts Do

`scripts/build-image.sh`:

1. Builds [`Dockerfile`](Dockerfile), using ExtendDB's `main` branch by
   default and compiling it with
   `cargo build --release --no-default-features --features mongodb`
   (ExtendDB's `postgres`/`mongodb`/`sqlite` backends are mutually exclusive
   at compile time — a build enabling more than one is rejected). Set
   `EXTENDDB_REF` to build another remote ref, or `EXTENDDB_SOURCE_DIR` to
   build an uncommitted local ExtendDB working tree.
2. If a kind cluster is active in your current `kubectl` context, loads the
   image directly with `kind load docker-image` so no registry push is
   needed. For non-kind clusters, push the image yourself and set
   `EXTENDDB_IMAGE` before running `deploy.sh`.

`scripts/deploy.sh`:

1. Creates the `extenddb` namespace and a 1Gi PVC for ExtendDB's generated
   config (`extenddb.toml`) and self-signed TLS certificate.
2. Reads the connection string from the `DocumentDB` resource's
   `status.connectionString`, reads the referenced credentials directly from
   the `docdb-credentials` Secret, and swaps the ClusterIP for the in-cluster
   DNS name.
3. **Strips `replicaSet=rs0`** while retaining `directConnection=true` (see
   [Troubleshooting](#replicaset-vs-directconnection) below), then adds
   `tlsAllowInvalidCertificates=true` for the playground gateway's self-signed
   certificate.
4. Stores the resolved URI in an `extenddb-mongo-uri` Secret.
5. Runs a one-shot `extenddb init --backend mongodb` Job against that URI,
   waits for it to complete, and prints the generated admin credentials from
   the Job logs (shown once by ExtendDB — save them).
6. Deploys the long-running `extenddb serve --foreground` Deployment and a
   ClusterIP Service on port `18443`.

`scripts/test-connection.sh`:

Port-forwards to the ExtendDB Service and runs
CreateTable → PutItem → GetItem → UpdateItem → DeleteItem →
TransactWriteItems → TransactGetItems → DeleteTable through the AWS CLI to
confirm basic CRUD and transactional round trips through DocumentDB. An exit
trap attempts to delete the test table if an intermediate check fails.

`scripts/cleanup.sh`:

Deletes the `extenddb` namespace. Leaves the DocumentDB instance untouched.

## Demo Program

[`demo/demo.py`](demo/demo.py) is a small, self-contained `boto3` script that
walks through the classic DynamoDB "Movies" tutorial table end to end:
`CreateTable`, `BatchWriteItem`, `GetItem`, `Query`, `UpdateItem`, `Scan`,
`DeleteItem`, and `DeleteTable` — all served by ExtendDB and persisted in
DocumentDB.

```bash
kubectl port-forward svc/extenddb 18443:18443 -n extenddb &
pip install -r demo/requirements.txt
EXTENDDB_ACCESS_KEY_ID=<from create-access-key above> \
EXTENDDB_SECRET_ACCESS_KEY=<from create-access-key above> \
    ./demo/demo.py
```

## Interactive Shell: ddbsh

There's no official "DynamoDB shell" from AWS, but
[`ddbsh`](https://github.com/awslabs/dynamodb-shell) (AWS Labs' open-source
DynamoDB Shell) is the closest equivalent to `mongosh`/`psql` — a readline
REPL that accepts SQL-like DDL/DML (`SELECT`, `INSERT`, `CREATE TABLE`,
`DESCRIBE`, etc.) and talks the same DynamoDB wire protocol ExtendDB serves.

Install (macOS):

```bash
brew tap aws/tap
brew install aws-ddbsh
```

For Linux, grab a prebuilt binary from the latest "Build DynamoDB Shell"
GitHub Actions run, or build from source (see the
[project README](https://github.com/awslabs/dynamodb-shell) for
prerequisites — cmake, a C++ compiler, and the AWS C++ SDK).

Point it at ExtendDB with the SigV4 credentials created in
[Creating a DynamoDB API access key](#creating-a-dynamodb-api-access-key),
and override the endpoint via `DDBSH_ENDPOINT_OVERRIDE`:

```bash
kubectl port-forward svc/extenddb 18443:18443 -n extenddb &

export AWS_ACCESS_KEY_ID=<from create-access-key>
export AWS_SECRET_ACCESS_KEY=<from create-access-key>
export AWS_DEFAULT_REGION=us-east-1
export DDBSH_ENDPOINT_OVERRIDE="https://127.0.0.1:18443"

ddbsh
# ddbsh - version 0.5.1
# us-east-1 (*)> select * from "Movies";
```

You can also switch endpoints from inside a running shell session with
`CONNECT region WITH ENDPOINT endpoint;`. The `(*)` in the prompt confirms
you're pointed at a non-standard endpoint rather than real AWS DynamoDB.

> **TLS note:** ddbsh uses the AWS C++ SDK's standard TLS verification and
> has no `--no-verify-ssl` equivalent. Against ExtendDB's self-signed
> certificate you'll need to trust it system-wide (e.g. add
> `~/.extenddb/tls/cert.pem`, or the cert extracted from the `extenddb-state`
> PVC, to your OS/OpenSSL trust store) rather than disabling verification
> as the Python demo and `test-connection.sh` do.

## Configuration

### Using a different ExtendDB version

`scripts/build-image.sh` accepts environment variables for a remote source
ref without pinning the playground:

```bash
EXTENDDB_REF=v0.1.3 ./scripts/build-image.sh
```

To validate an uncommitted local ExtendDB working tree without loading an
image into a cluster, build it directly as the named source context:

```bash
docker build \
  --build-context extenddb-source=/absolute/path/to/extenddb \
  -t extenddb-mongo:playground .
```

### Connecting from outside the cluster

By default the `extenddb` Service is `ClusterIP`. For external access, either
`kubectl port-forward` (as `test-connection.sh` does) or edit
[`manifests/serve.yaml`](manifests/serve.yaml) to set `type: LoadBalancer`.

### TLS

ExtendDB requires TLS and refuses to start without it; `extenddb init`
auto-generates a self-signed certificate. `test-connection.sh` uses
`aws ... --no-verify-ssl` for simplicity. For a more realistic setup, extract
the generated certificate from the PVC (or the init Job's logs directory
`/var/lib/extenddb`) and pass it via `AWS_CA_BUNDLE` instead.

## Troubleshooting

### `replicaSet` vs. `directConnection`

DocumentDB's printed connection string sets both `directConnection=true` and
`replicaSet=rs0`. Some MongoDB drivers (e.g. the Go driver used by KEDA,
`pymongo` as used by the [LightRAG playground](../lightrag/)) fail when both
are combined, because the driver validates the gateway's advertised
replica-set name against the requested one and rejects the mismatch —
producing errors like *"client is configured to connect to a replica set
named 'rs0' but this node belongs to a set named 'None'"*. Those playgrounds
work around it by dropping `replicaSet=rs0`.

ExtendDB's docs describe its MongoDB backend as **requiring** a replica set
(even single-node deployments), which initially suggested `replicaSet=rs0`
couldn't be dropped the way other playgrounds drop it. **This was tested
empirically, twice, with different results** — the second, authoritative
test is what `scripts/deploy.sh` implements:

| Test                                                              | Connection string                                | Result |
| ------------------------------------------------------------------ | --------------------------------------------------- | -------- |
| `mongosh` (Node.js driver) against the DocumentDB gateway directly | `directConnection=true` **and** `replicaSet=rs0`     | ✅ Succeeds |
| `mongosh` against the gateway                                       | `replicaSet=rs0` alone (no `directConnection`)      | ❌ Fails — 30s server selection timeout |
| **Real `extenddb init --backend mongodb` binary** (Rust `mongodb` v3 driver) against the gateway | `directConnection=true` **and** `replicaSet=rs0` | ❌ **Fails** — `Connection string replicaSet name "rs0" does not match actual name <none>` |
| **Real `extenddb init --backend mongodb` binary**                  | `directConnection=true`, **no** `replicaSet`         | ✅ **Succeeds** |

The gateway identifies itself via `hello`/`isMaster` as `isdbgrid` (mongos-
style) with no `setName` field at all. mongosh's driver skips validating a
requested `replicaSet` against the (nonexistent) advertised one once
`directConnection=true` is set — but the Rust `mongodb` v3 driver ExtendDB
actually links does **not** skip that validation, and rejects the mismatch
outright even in direct-connection mode. The mongosh result was a false
positive for this specific driver/version combination — the real ExtendDB
binary is the authoritative test.

So, same as the lightrag and keda-autoscaling playgrounds in this repo (and
for the same underlying reason), `scripts/deploy.sh` strips `replicaSet=rs0`
from the connection string before handing it to ExtendDB. This has not
prevented ExtendDB from using MongoDB transactions against DocumentDB in
testing — DocumentDB's gateway appears to support the driver's transaction
commands regardless of what replica-set name (if any) the client requested.
If you still see connection or topology errors from the `extenddb-init` Job
or the `extenddb` Deployment, check the resolved connection string in the
`extenddb-mongo-uri` Secret first:

```bash
kubectl get secret extenddb-mongo-uri -n extenddb \
    -o jsonpath='{.data.connection_string}' | base64 -d
```

DocumentDB's gateway also uses a self-signed TLS certificate by default,
which the Rust `mongodb` driver's default verifier rejects with `invalid
peer certificate: UnknownIssuer`; `scripts/deploy.sh` appends
`tlsAllowInvalidCertificates=true` to the connection string to work around
this (playground/demo only — see [`../tls/`](../tls/) for production-grade
TLS trust setups).

### Init Job fails or admin credentials were lost

`extenddb init` refuses to run twice against the same catalog/data
databases. To start over:

```bash
kubectl delete job extenddb-init -n extenddb --ignore-not-found
kubectl exec -n extenddb deploy/extenddb -- extenddb destroy --config /var/lib/extenddb/extenddb.toml --yes
kubectl delete deployment extenddb -n extenddb
./scripts/deploy.sh
```

### Table/Item operations return errors referencing transactions

ExtendDB's MongoDB backend uses multi-document transactions for some
operations (e.g. `TransactWriteItems`). Confirm your DocumentDB extension
version supports the MongoDB transaction commands ExtendDB issues; check
`kubectl logs deploy/extenddb -n extenddb` for the underlying MongoDB error.

### Pod is "Ready" but requests still fail

`extenddb healthcheck` (used for both probes) checks that the process/
listener is up; it does not necessarily verify DocumentDB connectivity was
established successfully after startup. Treat a Ready pod as necessary but
not sufficient — run `scripts/test-connection.sh` as the real functional
check, and check `kubectl logs deploy/extenddb -n extenddb` for MongoDB
connection errors if it fails.

### `Multi-Attach error for volume` on multi-node clusters

The init Job and serve Deployment share one ReadWriteOnce PVC and are
applied sequentially by `deploy.sh` to avoid this, but on a multi-node
cluster the completed init Job's pod can briefly hold the volume attached to
its node after `Complete`, causing the Deployment's pod to fail scheduling
on a different node. The Job sets `ttlSecondsAfterFinished: 600` so it's
cleaned up automatically, but if you hit this, delete the finished Job pod
manually and let the Deployment retry:

```bash
kubectl delete pod -n extenddb -l job-name=extenddb-init
```

This is not an issue on single-node kind clusters.

## Cleanup

```bash
./scripts/cleanup.sh                          # removes the extenddb namespace
kubectl delete namespace documentdb-test      # removes DocumentDB (optional)
```
