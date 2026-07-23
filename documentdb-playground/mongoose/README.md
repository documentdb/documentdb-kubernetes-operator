# Mongoose with DocumentDB

This playground shows how to use [Mongoose](https://mongoosejs.com/), the most
popular MongoDB ODM for Node.js, against DocumentDB. It includes:

- a small **Express + Mongoose REST API** (`app/`), and
- a standalone **Mongoose CRUD/compatibility test suite**
  (`app/mongoose-crud-test.js`) that exercises connect, schema/index creation,
  insert, query, update, aggregation, unique-index enforcement, and delete.

**The primary path is to run the app and test suite locally on your machine**,
connecting to a DocumentDB instance running in a cluster (local kind or remote
AKS) through a `kubectl port-forward`. No image build or in-cluster deployment
is required. Deploying the app *into* the cluster is an optional path documented
later.

Use it to validate that your application's Mongoose models behave correctly
against the DocumentDB gateway.

> **What is Mongoose?** Mongoose is a **Node.js library** (an ODM, Object Data
> Modeling layer), not a CLI tool or a server. Your application imports it to
> define schemas/models and talk to a MongoDB-compatible database. In this
> playground the library is used by two things: the demo **app**
> ([`app/server.js`](app/server.js)) and the standalone **test script**
> ([`app/mongoose-crud-test.js`](app/mongoose-crud-test.js)). Configure both via
> the environment variables in [Configuration Reference](#configuration-reference).

## Architecture

The app runs **locally**; only DocumentDB runs in the cluster. A `kubectl
port-forward` bridges `localhost` to the in-cluster gateway service.

```
   Your machine                          Kubernetes cluster (kind or AKS)
┌────────────────────┐               ┌──────────────────────────────────────┐
│  mongoose app /    │   kubectl     │  ┌────────────────┐   ┌────────────┐  │
│  test script       │  port-forward │  │  DocumentDB    │──▶│ PostgreSQL │  │
│  (Node + Mongoose) │──────────────▶│  │  (Gateway)     │   │  (CNPG)    │  │
│  localhost:10260   │   (TLS, wire  │  └────────────────┘   └────────────┘  │
└────────────────────┘    protocol)  └──────────────────────────────────────┘
```

Mongoose connects to the DocumentDB gateway exactly as it would to a standalone
`mongod`, with three required tweaks (see [Connecting Mongoose to
DocumentDB](#connecting-mongoose-to-documentdb)).

## Prerequisites

- `node` + `npm` (to run the app and test suite locally), **primary path**
- A Kubernetes cluster with the [DocumentDB operator](../../README.md) installed
  (local kind, or AKS; see [`../aks-setup/`](../aks-setup/README.md))
- `kubectl` configured to point at that cluster
- `docker` + `kind`, **only** for the optional in-cluster deploy path

## Quick Start (run app locally)

> **Before applying `documentdb.yaml`**, edit it and replace the placeholder
> password (`ChangeMe!ReplaceBeforeUsing`) with a real one.

```bash
# 1. Deploy a DocumentDB instance into your cluster (skip if you already have one).
#    Works the same whether the cluster is local kind or remote AKS.
kubectl create namespace documentdb-test
kubectl apply -f documentdb.yaml
kubectl wait --for=jsonpath='{.status.status}'="Cluster in healthy state" \
    documentdb/documentdb-cluster -n documentdb-test --timeout=300s

# 2. Run the Mongoose app LOCALLY (port-forwards DocumentDB for you).
#    Leave this running; open a second terminal for the curl/test commands.
./scripts/run-app.sh

# 3. In another terminal, run the Mongoose CRUD/compatibility test suite LOCALLY.
./scripts/run-test.sh
```

`scripts/run-app.sh` and `scripts/run-test.sh` default to
`DOCUMENTDB_NAMESPACE=documentdb-test` and
`DOCUMENTDB_CLUSTER=documentdb-cluster`. Override via env vars if your cluster
uses different names:

```bash
DOCUMENTDB_NAMESPACE=my-ns DOCUMENTDB_CLUSTER=my-cluster ./scripts/run-app.sh
```

Both scripts work identically against a local kind cluster or a remote AKS
cluster, they only rely on your current `kubectl` context.

## Trying the API

With `./scripts/run-app.sh` running, the API is on `http://localhost:3000`:

```bash
# Health
curl -s http://localhost:3000/health
# {"status":"healthy","db":"connected"}

# Create a book
curl -s -X POST http://localhost:3000/books \
  -H 'Content-Type: application/json' \
  -d '{"title":"Dune","author":"Herbert","genres":["sci-fi"],"pages":412,"rating":5}'

# List books
curl -s http://localhost:3000/books | jq .

# Count books per genre (aggregation)
curl -s http://localhost:3000/stats/genres | jq .
```

## Optional: Deploy the App In-Cluster

If you want the app running *inside* the cluster instead of on your machine
(e.g. to measure in-cluster latency, or to avoid a long-lived port-forward),
use `scripts/deploy.sh`. This builds a container image, loads it into kind,
wires up the connection string as a Secret, and deploys a Deployment + Service.

```bash
./scripts/deploy.sh
kubectl port-forward svc/mongoose-demo -n mongoose-demo 3000:3000   # to reach it
./scripts/cleanup.sh                                                # remove it
```

> **AKS note:** `deploy.sh` auto-loads the image into **kind** only. On AKS,
> nodes can't see a locally built image; push it to a registry (e.g. ACR) and
> set `IMAGE` to that pushed tag before running `deploy.sh`. For most testing,
> the local path above avoids this entirely.

## Connecting Mongoose to DocumentDB

The DocumentDB gateway speaks the MongoDB wire protocol but advertises itself as
a **standalone** server over **TLS**. Mongoose therefore needs three options
(see [`app/db.js`](app/db.js)):

```js
await mongoose.connect(uri, {
  directConnection: true,          // gateway is standalone, not a replica set
  tls: true,                       // gateway only accepts TLS
  tlsAllowInvalidCertificates: true, // default install uses a self-signed cert
});
```

Additionally, **strip `replicaSet=rs0`** from the connection string. The
operator-supplied connection string sets both `directConnection=true` and
`replicaSet=rs0`; the Node driver treats these as conflicting and fails with
`client is configured to connect to a replica set named 'rs0' but this node
belongs to a set named 'None'`. Both `app/db.js` and the deploy scripts strip it
automatically.

For production, set `TLS_INSECURE=false`, mount the cluster CA, and pass
`tlsCAFile` instead of `tlsAllowInvalidCertificates`.

## Configuration Reference

All settings are passed via environment variables; there is no config file.

### App + test script (`app/`)

Read by [`app/db.js`](app/db.js), [`app/server.js`](app/server.js), and
[`app/mongoose-crud-test.js`](app/mongoose-crud-test.js).

| Variable                      | Default          | Used by        | Description                                                                                  |
| ----------------------------- | ---------------- | -------------- | -------------------------------------------------------------------------------------------- |
| `MONGO_URI`                   | _(required)_     | app + test     | DocumentDB connection string. `replicaSet=rs0` is stripped automatically. The test script also accepts it as the first CLI argument. |
| `MONGO_DB`                    | `mongoose_demo` (app), `mongoose_test` (test) | app + test | Database name Mongoose connects to.                                                          |
| `TLS_INSECURE`                | `true`           | app + test     | When `true`, accepts the gateway's self-signed cert (`tlsAllowInvalidCertificates`). Set `false` for CA-verified TLS in production. |
| `SERVER_SELECTION_TIMEOUT_MS` | `10000`          | app            | How long Mongoose waits to select a server before erroring.                                  |
| `PORT`                        | `3000`           | app            | Port the Express API listens on.                                                             |

These map directly to the Mongoose connect options in
[`app/db.js`](app/db.js): `directConnection: true`, `tls: true`,
`tlsAllowInvalidCertificates`, `serverSelectionTimeoutMS`, and `autoIndex`.

### Scripts (`scripts/`)

Read by [`deploy.sh`](scripts/deploy.sh), [`run-test.sh`](scripts/run-test.sh),
and [`cleanup.sh`](scripts/cleanup.sh).

| Variable               | Default              | Used by              | Description                                                            |
| ---------------------- | -------------------- | -------------------- | --------------------------------------------------------------------- |
| `DOCUMENTDB_NAMESPACE` | `documentdb-test`    | run-app, run-test, deploy | Namespace of the DocumentDB resource.                            |
| `DOCUMENTDB_CLUSTER`   | `documentdb-cluster` | run-app, run-test, deploy | Name of the `DocumentDB` custom resource.                        |
| `LOCAL_PORT`           | `10260`              | run-app, run-test    | Local port used for the temporary `kubectl port-forward`.             |
| `PORT`                 | `3000`               | run-app              | Local port the Express API listens on.                                |
| `APP_NAMESPACE`        | `mongoose-demo`      | deploy, cleanup      | Namespace the in-cluster app is deployed into (optional path).        |
| `IMAGE`                | `documentdb/mongoose-demo:local` | deploy   | Image tag to build and deploy (optional path).                        |
| `KIND_CLUSTER`         | _(auto-detected)_    | deploy               | kind cluster to load the image into. Set when running multiple clusters. |

## DocumentDB Compatibility Notes

| Mongoose feature            | Status with DocumentDB | Notes                                                                 |
| --------------------------- | ---------------------- | --------------------------------------------------------------------- |
| CRUD (`create`/`find`/…)    | ✅ Supported           | Standard document operations work as expected.                        |
| `autoIndex` / `syncIndexes` | ✅ Supported           | Avoid `collation` on indexes; DocumentDB does not implement them.    |
| Unique indexes              | ✅ Supported           | Duplicate keys raise the standard `E11000` error.                     |
| Aggregation pipelines       | ✅ Common stages       | `$match`, `$group`, `$unwind`, `$sort`, etc. Atlas-only stages differ.|
| `findById` / `_id` lookups  | ⚠️ Known gateway bug   | Filtering on `_id` fails with `trying to open a pruned relation` on the gateway bundled with operator `0.2.0` (gateway `0.109.0`). Fixed upstream but not yet in a released, operator-compatible gateway image. Query by another field as a workaround. |
| `$vectorSearch`             | ❌ Not supported       | Atlas-only operator; not implemented by DocumentDB.                   |
| Index `collation`           | ❌ Not supported       | `createIndex.collation is not implemented yet`; omit it.             |
| Change streams              | ⚠️ Check version       | Verify against your DocumentDB version before relying on it.          |

The CRUD test suite ([`app/mongoose-crud-test.js`](app/mongoose-crud-test.js))
covers the supported rows above and prints a pass/fail summary.

## Running the Test Suite Manually

`scripts/run-test.sh` port-forwards DocumentDB to `localhost` and runs the suite
for you. To run it directly against any reachable connection string (for
example, an AKS DocumentDB exposed via a `LoadBalancer` external IP):

```bash
cd app
npm install
MONGO_URI="mongodb://user:pass@HOST:10260/?tls=true&tlsAllowInvalidCertificates=true&directConnection=true" \
  node mongoose-crud-test.js
```

Expected output:

```
Mongoose DocumentDB compatibility test
======================================
  ✅ connect
  ✅ create indexes (autoIndex / syncIndexes)
  ✅ insertOne (Model.create)
  ✅ insertMany
  ⚠️  findById: known DocumentDB issue (trying to open a pruned relation)
  ...
======================================
Passed: 12  Failed: 0  Known issues: 1
```

> **Note:** On operator `0.2.0` (gateway `0.109.0`) the `findById` step hits a
> known gateway bug for `_id` point lookups (`trying to open a pruned relation`).
> The suite reports it as a **known issue** (⚠️) rather than a failure, so the
> run still exits green (`Failed: 0`). If a fixed gateway release is deployed,
> the step passes and is counted as a normal pass. See the compatibility table
> above and the troubleshooting entry below.

## What the Scripts Do

| Script                  | Purpose                                                                                  |
| ----------------------- | ---------------------------------------------------------------------------------------- |
| `scripts/run-app.sh`    | **Primary.** Port-forwards DocumentDB and runs the Express + Mongoose app locally.       |
| `scripts/run-test.sh`   | **Primary.** Port-forwards DocumentDB and runs the Mongoose CRUD/compatibility suite locally. |
| `scripts/deploy.sh`     | Optional. Builds the demo image, loads it into kind, wires the connection string into a Secret, and deploys the app in-cluster. |
| `scripts/cleanup.sh`    | Removes the in-cluster app, Secret, and namespace (leaves DocumentDB running).           |
| `scripts/lib.sh`        | Shared helpers: resolve the `MONGO_URI` from the DocumentDB status and manage the port-forward. |

## Troubleshooting

### App pod is `CrashLoopBackOff` with a `replicaSet 'rs0'` error

The connection string still contains `replicaSet=rs0`. The scripts strip it
automatically; if you set `MONGO_URI` manually, remove that query parameter.

### `MongooseServerSelectionError` / TLS handshake failures

The gateway requires TLS. Confirm `tls: true` is set and, for the default
self-signed install, `tlsAllowInvalidCertificates: true` (or `TLS_INSECURE`
unset/`true`). Verify the gateway is reachable:

```bash
kubectl run mongo-test --rm -it --restart=Never -n documentdb-test --image=mongo:7 \
  --command -- mongosh "<your-connection-string>" --eval 'db.adminCommand({ping:1})'
```

### `trying to open a pruned relation` on `findById` / `_id` queries

This is a known bug in the gateway bundled with operator `0.2.0`
(gateway `0.109.0`): any query that filters on `_id` (for example Mongoose
`findById` or `GET /books/:id`) fails with this error, while queries on other
fields work. The fix exists upstream but has not yet been published as an
operator-compatible gateway release tag. Until then, look documents up by
another indexed field (for example a unique `sku`/business key). Once a fixed
gateway release is available, pin it via `spec.gatewayImage` in
[`documentdb.yaml`](documentdb.yaml).

### `createIndex.collation is not implemented yet`

A schema index uses `collation`. Remove it; DocumentDB does not support
collation indexes. The models in this playground intentionally avoid it.

### Image not found when deploying to kind

The deploy script auto-detects the first kind cluster. If you run multiple
clusters, set the target explicitly:

```bash
KIND_CLUSTER=my-cluster ./scripts/deploy.sh
```

## Directory Layout

```
mongoose/
├── README.md
├── documentdb.yaml              # Sample DocumentDB instance
├── app/
│   ├── package.json
│   ├── db.js                    # Mongoose connection (DocumentDB options)
│   ├── server.js                # Express REST API (/books, /health, /stats)
│   ├── models/book.js           # Example Mongoose schema/model
│   ├── mongoose-crud-test.js    # Standalone CRUD/compatibility test suite
│   └── Dockerfile
├── k8s/
│   └── mongoose-app.yaml        # Deployment + Service (optional in-cluster path)
└── scripts/
    ├── lib.sh                   # Shared connection-string resolver + port-forward
    ├── run-app.sh               # Primary: run the app locally
    ├── run-test.sh              # Primary: run the test suite locally
    ├── deploy.sh                # Optional: deploy the app in-cluster
    └── cleanup.sh
```
