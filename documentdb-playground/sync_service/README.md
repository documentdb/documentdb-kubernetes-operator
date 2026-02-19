# DocumentDB On-Premise ↔ Azure DocumentDB Sync Service

Sync data from on-premise [DocumentDB][documentdb-oss] to [Azure DocumentDB][azure-documentdb] in real time — with **zero data loss**, even through crashes and connectivity outages.

🏪 DocumentDB on Kubernetes → 🔄 Change stream sync → ☁️ Azure DocumentDB

> 📺 **[Watch the demo video](https://youtu.be/CKEbGhgRhiQ)**

## Features

- **Collection-level change stream**: Watches specific collections for changes
- **Crash recovery via resume tokens**: Persists [resume tokens](https://www.mongodb.com/docs/manual/changeStreams/#resume-a-change-stream) to disk so the service can resume from where it left off after a restart or crash, without missing or duplicating changes
- **Idempotent sync**: Uses upserts to handle replays safely
- **Automatic retry**: Exponential backoff on transient failures
- **Graceful shutdown**: Handles SIGINT/SIGTERM for clean exits

## Prerequisites

- A Kubernetes cluster with the [DocumentDB operator installed][getting-started]
- **Python 3.8+** with pip
- [mongosh][mongosh] or [DocumentDB for VS Code][vscode-documentdb] (for testing)

## Setup

### 1. Create a DocumentDB cluster with change streams enabled

> **Note:** Change streams are not yet officially supported. This playground uses custom DocumentDB and gateway images built from a [custom branch][changestream-branch]. When the `ChangeStreams` feature gate is enabled, the operator automatically uses these images — no manual image configuration needed.

Follow the [Getting Started guide][getting-started] to install the operator, deploy a DocumentDB cluster and set up port forwarding. When creating the cluster, enable the `ChangeStreams` feature gate:

```sh
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: documentdb-preview
  namespace: documentdb-preview-ns
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  featureGates:
    ChangeStreams: true
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
EOF
```

### 2. Create [Azure DocumentDB (with MongoDB compatibility)][azure-documentdb]

Create an Azure DocumentDB cluster using any of these methods:
[Azure Portal](https://learn.microsoft.com/azure/documentdb/quickstart-portal) · [Azure CLI][az-cosmosdb-mongocluster] · [Bicep / ARM](https://learn.microsoft.com/azure/documentdb/quickstart-bicep) · [Terraform](https://learn.microsoft.com/azure/documentdb/quickstart-terraform)

<details>
<summary>Example: Azure CLI</summary>

```bash
az login
az account set --subscription "<your-subscription-id>"

az cosmosdb mongocluster create \
  --cluster-name documentdb-sync-target \
  --resource-group <your-resource-group> \
  --location eastus \
  --administrator-login <your-admin-user> \
  --administrator-login-password <your-admin-password> \
  --server-version 5.0 \
  --shard-node-tier "M30" \
  --shard-node-ha true \
  --shard-node-disk-size-gb 128 \
  --shard-node-count 1

# Get connection string
az cosmosdb mongocluster show \
  --cluster-name documentdb-sync-target \
  --resource-group <your-resource-group> \
  --query "connectionString" -o tsv
```
</details>

### 3. Install dependencies

```bash
pip install -r requirements.txt
```

### 4. Configure the sync service

Edit `config.yaml` and fill in the following required fields:

| Field | Description |
|-------|-------------|
| `source.uri` | Connection string to your source DocumentDB instance (created by the Kubernetes Operator) |
| `target.uri` | Connection string to your target [Azure DocumentDB (with MongoDB compatibility)][azure-documentdb] |
| `watch.collections` | List of collections to sync, in `database.collection` format |

Example:

```yaml
source:
  uri: "mongodb://k8s_secret_user:K8sSecret100@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"

target:
  uri: "mongodb+srv://admin:MyP%40ss@target-cluster.mongocluster.cosmos.azure.com/?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000"

watch:
  collections:
    - demodb.demo_orders
```

**Optional settings** (with defaults):

| Field | Default | Description |
|-------|---------|-------------|
| `state.persist_interval` | `10` | Persist resume tokens to disk every N changes. Tokens are also flushed when the stream is idle, so no changes are lost. |
| `logging.level` | `INFO` | Log level (`DEBUG`, `INFO`, `WARNING`, `ERROR`) |

## Usage

### Start the sync service

```bash
python sync.py --config config.yaml
```

### Reset state and start fresh

The `--reset` flag deletes the state file that stores resume tokens, so the sync will only capture changes that occur after the service starts rather than resuming from where it previously left off.

```bash
python sync.py --config config.yaml --reset
```

### Stop gracefully

Press `Ctrl+C` or send SIGTERM. The service will:
1. Finish processing current change
2. Persist the resume token
3. Close connections cleanly

## Demo Walkthrough

This walkthrough uses three terminals side by side.

| Terminal | Purpose | Connect with [mongosh][mongosh] |
|----------|---------|----------------------------------|
| **Source** | On-premise DocumentDB on Kubernetes | `mongosh "mongodb://k8s_secret_user:K8sSecret100@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"` |
| **Target** | [Azure DocumentDB][azure-documentdb] | `mongosh "<your-azure-documentdb-connection-string>"` |
| **Sync service** | Runs the Python sync service | — |

> You can also use the [DocumentDB for VS Code][vscode-documentdb] extension instead of mongosh.

In **both** the source and target shells, switch to the demo database:

```javascript
use demodb
```

### 1. Verify both databases are empty

Run in **both** source and target shells:

```javascript
db.demo_orders.find()
```

Expected: both return an empty result.

### 2. Start the sync service

In the **sync service** terminal:

```bash
cd documentdb-playground/sync_service
python sync.py --config config.yaml
```

Wait for the `Watching for changes...` message — the service is now connected and ready.

### 3. Live INSERT

In the **source** shell, insert an order:

```javascript
db.demo_orders.insertOne({
  orderId: "ORD-001",
  customer: "Microsoft",
  product: "Widget",
  amount: 1500,
  status: "pending"
})
```

Verify in the **target** shell:

```javascript
db.demo_orders.find()
```

The document appears in [Azure DocumentDB][azure-documentdb] — synced within milliseconds. You should see the `ORD-001` document.

### 4. Live UPDATE

In the **source** shell, update the order:

```javascript
db.demo_orders.replaceOne(
  { orderId: "ORD-001" },
  {
    orderId: "ORD-001",
    customer: "Microsoft",
    product: "Widget",
    amount: 2000,
    status: "completed"
  }
)
```

Verify in the **target** shell:

```javascript
db.demo_orders.find()
```

The status is now `completed` and the amount is updated to `2000`.

### 5. Crash recovery — zero data loss

This is the key feature. What happens if the sync service goes down? Do we lose data?

**Stop the sync service** — press `Ctrl+C` to simulate a crash.

**Insert data while the service is down** — in the **source** shell:

```javascript
db.demo_orders.insertMany([
  { orderId: "ORD-002", customer: "Beta Corp", amount: 500, status: "pending" },
  { orderId: "ORD-003", customer: "Gamma Inc", amount: 750, status: "pending" },
  { orderId: "ORD-004", customer: "Delta LLC", amount: 1200, status: "pending" }
])
```

**Verify the target has NOT received them** — in the **target** shell:

```javascript
db.demo_orders.find()
```

Only `ORD-001` is present. The three new orders are not synced because the service was down.

**Restart the sync service:**

```bash
python sync.py --config config.yaml
```

The service uses its saved resume token to pick up **exactly** where it left off. 

**Verify all documents arrived** — in the **target** shell:

```javascript
db.demo_orders.find()
```

All four orders (`ORD-001` through `ORD-004`) are now present. Zero data loss, even during failures — this is the power of change streams with resume tokens.

## How Resume Tokens Work

The sync service persists a **resume token** after each synced change. On restart, it passes the token to the change stream via `resume_after`, so the stream picks up exactly where it left off.

1. Each change event contains a resume token in its `_id` field
2. The token is saved to a state file after every sync
3. On restart, the saved token tells the change stream where to resume

### State file

Auto-generated in the sync service directory:

```
.documentdb_sync_state_<source-cluster>_to_<target-cluster>.json
```

## Resources

- ☁️ [Azure DocumentDB][azure-documentdb]
- 🌐 [Open-source DocumentDB][documentdb-oss]
- ⚙️ [DocumentDB Kubernetes Operator][documentdb-k8s-operator]
- 💬 [Join us on Discord][discord]

<!-- Reference links -->
[azure-documentdb]: https://learn.microsoft.com/azure/documentdb/
[documentdb-oss]: https://github.com/documentdb/documentdb
[documentdb-k8s-operator]: https://github.com/documentdb/documentdb-kubernetes-operator
[discord]: https://discord.gg/WfTZxRh9qX
[getting-started]: ../../docs/operator-public-documentation/preview/index.md
[changestream-branch]: https://github.com/WentingWu666666/documentdb/tree/users/wentingwu/changefeed_vibe
[mongosh]: https://www.mongodb.com/docs/mongodb-shell/install/
[vscode-documentdb]: https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-documentdb
[az-cosmosdb-mongocluster]: https://learn.microsoft.com/cli/azure/cosmosdb/mongocluster?view=azure-cli-latest