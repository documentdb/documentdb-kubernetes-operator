# DocumentDB to Azure DocumentDB (with MongoDB compatibility) Sync Service

A Python-based change stream sync service that replicates data from a DocumentDB instance (created by the DocumentDB Kubernetes Operator) to Azure DocumentDB (with MongoDB compatibility).

## Features

- **Collection-level change stream**: Watches specific collections for changes
- **Crash recovery**: Persists resume tokens for catching up after restarts
- **Idempotent sync**: Uses upserts to handle replays safely
- **Automatic retry**: Exponential backoff on transient failures
- **Graceful shutdown**: Handles SIGINT/SIGTERM for clean exits

## Prerequisites

1. **Source DocumentDB** created by the DocumentDB Kubernetes Operator with change streams enabled
2. **Azure DocumentDB (with MongoDB compatibility)** account created
3. **Python 3.8+** with pip

## Setup

### 1. Install dependencies

```bash
pip install -r requirements.txt
```

### 2. Create Azure DocumentDB (with MongoDB compatibility) (run on your local machine with Azure CLI)

```bash
# Login to Azure
az login
az account set --subscription "<your-subscription-id>"

# Create Azure DocumentDB (with MongoDB compatibility) cluster
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

### 3. Configure the sync service

Edit `config.yaml` and fill in the following required fields:

| Field | Description | Example |
|-------|-------------|---------|
| `source.uri` | Connection string to your source DocumentDB instance (created by the Kubernetes Operator) | `mongodb+srv://user:pass@my-cluster.mongocluster.cosmos.azure.com/?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000` |
| `target.uri` | Connection string to your target Azure DocumentDB (with MongoDB compatibility) | `mongodb+srv://user:pass@my-target.mongocluster.cosmos.azure.com/?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000` |
| `watch.collections` | List of collections to sync, in `database.collection` format | `mydb.orders`, `mydb.users` |

Example:

```yaml
source:
  uri: "mongodb+srv://admin:MyP%40ss@source-cluster.mongocluster.cosmos.azure.com/?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000"

target:
  uri: "mongodb+srv://admin:MyP%40ss@target-cluster.mongocluster.cosmos.azure.com/?tls=true&authMechanism=SCRAM-SHA-256&retrywrites=false&maxIdleTimeMS=120000"

watch:
  collections:
    - mydb.orders
    - mydb.users
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

```bash
python sync.py --config config.yaml --reset
```

**Warning**: Resetting may cause duplicate data if documents already exist in target.

### Stop gracefully

Press `Ctrl+C` or send SIGTERM. The service will:
1. Finish processing current change
2. Persist the resume token
3. Close connections cleanly

## How Resume Tokens Work

1. Each change event contains a resume token in its `_id` field
2. After syncing each change, the per-collection token is saved to the auto-generated state file
3. On restart, the service reads each collection's saved token and passes it as `resume_after`
4. The change stream resumes from exactly where it left off

### State file location

The state file is auto-generated based on source and target cluster names:

```
~/.documentdb_sync_state_<source-cluster>_to_<target-cluster>.json
```

Example contents:
```json
{
  "resume_tokens": {
    "mydb.orders": {"_data": "0100000001..."},
    "mydb.users": {"_data": "0100000002..."}
  },
  "last_sync_time": "2026-02-05T10:30:00Z",
  "sync_stats": {
    "total_synced": 1234,
    "inserts": 500,
    "updates": 400,
    "deletes": 334,
    "errors": 0
  }
}
```

## Testing the Sync

### Terminal 1: Start the sync service

```bash
python sync.py
```

### Terminal 2: Make changes on source via mongosh

Connect to your source DocumentDB and make some changes:

```javascript
// Insert
db.mycollection.insertOne({ name: "Test", value: 123 })

// Update
db.mycollection.updateOne({ name: "Test" }, { $set: { value: 456 } })

// Delete
db.mycollection.deleteOne({ name: "Test" })
```

### Verify in Azure DocumentDB (with MongoDB compatibility)

Use Azure Portal Data Explorer or mongosh to connect to your Azure DocumentDB (with MongoDB compatibility) and verify the documents were synced.

## Troubleshooting

### "Could not connect to database"

- Check that the source DocumentDB instance is running and accessible
- Verify credentials in config.yaml match the DocumentDB user credentials

### "Authentication/authorization error"

- Verify Azure DocumentDB connection string is correct
- Check that Azure DocumentDB firewall allows connections from your IP

### Sync seems stuck

- Check logs for errors
- Verify change stream is receiving events (try `db.collection.watch()` in mongosh)
- Check the auto-generated state file for last_sync_time

## Architecture

```
┌─────────────────────┐     Change Stream      ┌─────────────────────┐
│  Source DocumentDB  │ ──────────────────────▶│    Sync Service     │
│  (Created by        │                        │    (Python)         │
│   Operator)         │                        │                     │
│                     │                        │  - Watch collections│
└─────────────────────┘                        │  - Track tokens     │
                                               │  - Retry on fail    │
                                               └──────────┬──────────┘
                                                          │
                                                          │ Upsert/Delete
                                                          ▼
                                               ┌─────────────────────┐
                                               │ Azure DocumentDB    │
                                               │ (MongoDB compatible)│
                                               │                     │
                                               └─────────────────────┘
```
