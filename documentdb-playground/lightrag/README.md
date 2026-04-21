# LightRAG with DocumentDB

This playground deploys [LightRAG](https://github.com/HKUDS/LightRAG) — a graph-based Retrieval-Augmented Generation (RAG) engine — using DocumentDB as its MongoDB-compatible storage backend.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Kubernetes Cluster                  │
│                                                     │
│  ┌──────────────┐    ┌──────────────┐               │
│  │   LightRAG   │───▶│   Ollama     │               │
│  │  (RAG Engine)│    │ (LLM + Embed)│               │
│  └──────┬───────┘    └──────────────┘               │
│         │                                           │
│         │ MongoDB wire protocol                     │
│         ▼                                           │
│  ┌──────────────┐    ┌──────────────┐               │
│  │  DocumentDB  │───▶│  PostgreSQL  │               │
│  │  (Gateway)   │    │   (CNPG)     │               │
│  └──────────────┘    └──────────────┘               │
│                                                     │
│  Storage mapping:                                   │
│  ├─ KV storage      → MongoKVStorage (DocumentDB)   │
│  ├─ Graph storage   → MongoGraphStorage (DocumentDB) │
│  ├─ Doc status      → MongoDocStatusStorage (DocDB)  │
│  └─ Vector storage  → NanoVectorDBStorage (local)    │
└─────────────────────────────────────────────────────┘
```

LightRAG stores knowledge graph nodes, edges, document metadata, and LLM response caches in DocumentDB collections. Vector embeddings use local file-based storage because DocumentDB does not support the Atlas `$vectorSearch` operator.

## Prerequisites

- A running Kubernetes cluster with the DocumentDB operator installed
- A healthy DocumentDB instance (see [Quick Start](../../docs/operator-public-documentation/preview/index.md))
- [Helm](https://helm.sh/docs/intro/install/) v3.0+
- [kubectl](https://kubernetes.io/docs/tasks/tools/install-kubectl/) configured for your cluster

## Quick Start

```bash
# Deploy everything (Ollama + LightRAG) with default settings
./scripts/deploy.sh

# Clean up
./scripts/cleanup.sh
```

## Step-by-Step Deployment

### 1. Verify DocumentDB is Running

```bash
kubectl get documentdb --all-namespaces
# Expected: STATUS = "Cluster in healthy state"
```

### 2. Deploy Ollama (LLM Backend)

```bash
kubectl apply -f helm/ollama.yaml
kubectl wait --for=condition=ready pod -l app=ollama -n lightrag --timeout=120s

# Pull models (required on first run)
OLLAMA_POD=$(kubectl get pod -l app=ollama -n lightrag -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n lightrag "$OLLAMA_POD" -- ollama pull nomic-embed-text
kubectl exec -n lightrag "$OLLAMA_POD" -- ollama pull qwen2.5:3b
```

### 3. Deploy LightRAG

Edit `helm/lightrag-values.yaml` to set your DocumentDB connection string, then:

```bash
helm upgrade --install lightrag helm/lightrag \
  -n lightrag \
  -f helm/lightrag-values.yaml
```

### 4. Access the WebUI

```bash
kubectl port-forward svc/lightrag 9621:9621 -n lightrag
# Open http://localhost:9621
```

### 5. Test Document Ingestion

```bash
# Insert a document
curl -X POST http://localhost:9621/documents/text \
  -H "Content-Type: application/json" \
  -d '{"text": "Your document text here..."}'

# Query with graph-enhanced RAG
curl -X POST http://localhost:9621/query \
  -H "Content-Type: application/json" \
  -d '{"query": "What is this document about?", "mode": "hybrid"}'
```

## Configuration

### DocumentDB Connection

Update the `MONGO_URI` in `helm/lightrag-values.yaml`:

```yaml
env:
  MONGO_URI: "mongodb://<user>:<password>@<service>.<namespace>.svc.cluster.local:<port>/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true"
  MONGO_DATABASE: "LightRAG"
```

To get your connection string from the DocumentDB resource status:

```bash
# The connection string contains embedded kubectl commands for credentials.
# Use eval to resolve them into a usable URI.
RAW_CONN=$(kubectl get documentdb <cluster-name> -n <documentdb-namespace> -o jsonpath='{.status.connectionString}')
CONNECTION_STRING=$(eval "echo \"$RAW_CONN\"")
echo "$CONNECTION_STRING"
```

> **Note:** The `eval` command executes shell expansions in the connection string. This is safe when the string comes from your own DocumentDB resource, but never pipe untrusted input through `eval`.

### LLM Configuration

The default configuration uses [Ollama](https://ollama.com) with `qwen2.5:3b` for text generation and `nomic-embed-text` for embeddings. To use OpenAI instead:

```yaml
env:
  LLM_BINDING: openai
  LLM_MODEL: gpt-4o-mini
  LLM_BINDING_API_KEY: "sk-..."
  EMBEDDING_BINDING: openai
  EMBEDDING_MODEL: text-embedding-3-small
  EMBEDDING_DIM: "1536"
  EMBEDDING_BINDING_API_KEY: "sk-..."
```

### Storage Configuration

| Storage Type | Backend | Notes |
|---|---|---|
| KV Storage | `MongoKVStorage` | Documents, chunks, entities, relations |
| Graph Storage | `MongoGraphStorage` | Knowledge graph nodes and edges |
| Doc Status | `MongoDocStatusStorage` | Document processing state |
| Vector Storage | `NanoVectorDBStorage` | Local file-based (PVC) |

> **Why not MongoVectorDBStorage?** DocumentDB does not support the MongoDB Atlas `$vectorSearch` aggregation operator required by `MongoVectorDBStorage`. The file-based `NanoVectorDBStorage` works without limitations.

## DocumentDB Compatibility

LightRAG's MongoDB storage assumes MongoDB Atlas features. This playground includes an init container that patches the LightRAG code for DocumentDB compatibility:

| Feature | MongoDB Atlas | DocumentDB | Workaround |
|---|---|---|---|
| `$vectorSearch` | ✅ | ❌ | Use NanoVectorDBStorage |
| `$listSearchIndexes` | ✅ | ❌ | Graceful fallback to regex |
| `createIndex` with collation | ✅ | ❌ | Skip collation indexes |
| `createIndex` (secondary) | ✅ | Hangs | Skip via init-container patch |
| `find_one({'_id': id})` | ✅ | ⚠️ v0.109-0 bug | Upgrade to v0.110-0+ |
| Basic CRUD operations | ✅ | ✅ | Works natively |
| Aggregation pipelines | ✅ | ✅ | `$group`, `$match`, `$sort` work |

### How the Init-Container Patches Work

The Helm chart's `deployment.yaml` includes an init container (`patch-for-documentdb`) that runs before the main LightRAG container starts. It modifies LightRAG's MongoDB storage layer in-place to skip operations that are incompatible with DocumentDB.

**What gets patched:**

Three async methods in `lightrag/kg/mongo_impl.py` are stubbed out with an early `return`:

| Method | Why it's patched |
|---|---|
| `create_and_migrate_indexes_if_not_exists` | Calls `createIndex` with collation and secondary indexes. DocumentDB rejects collation (`"not implemented yet"`) and hangs indefinitely on secondary index creation. |
| `create_search_index_if_not_exists` | Calls `$listSearchIndexes` which DocumentDB doesn't support. While LightRAG catches the `PyMongoError` gracefully, skipping it avoids unnecessary error logs. |
| `create_vector_index_if_not_exists` | Creates Atlas `$vectorSearch` indexes. Not applicable because this playground uses `NanoVectorDBStorage` (local) instead of `MongoVectorDBStorage`. |

**How it works:**

1. The init container shares the same image as the main LightRAG container.
2. A Python script inserts `return` statements at the top of each method, effectively making them no-ops.
3. Both code locations are patched — `/app/lightrag/` (dev install) and `/app/.venv/lib/python3.12/site-packages/lightrag/` (venv install) — because the LightRAG Docker image includes two copies.
4. Bytecode caches (`__pycache__/mongo_impl*.pyc`) are cleared to prevent stale compiled code from being loaded.

**Impact:** LightRAG operates normally without indexes. All CRUD, aggregation, and graph traversal operations work correctly. The only trade-off is that queries on large datasets may be slower without secondary indexes, which is acceptable for a playground.

The patches are applied automatically — no manual configuration is needed.

## Verified Operations

The following LightRAG operations have been tested with DocumentDB v0.112.0+:

- ✅ Document ingestion and chunking
- ✅ Entity and relationship extraction (via LLM)
- ✅ Knowledge graph storage and traversal
- ✅ LLM response caching
- ✅ Naive, local, global, and hybrid RAG queries
- ✅ Document status tracking
- ✅ WebUI for graph visualization

> **Note:** DocumentDB v0.109-0 had a bug where `_id` lookups failed after writes. Use v0.110-0 or later.

## Testing Guide

Use this guide to verify LightRAG + DocumentDB integration is working correctly.

### Prerequisites

Ensure you have:
- DocumentDB operator installed
- LightRAG and Ollama pods running (`kubectl get pods -n lightrag`)
- Port-forward active: `kubectl port-forward svc/lightrag 9621:9621 -n lightrag &`
- Python with pymongo installed for verification tests

### Deploy DocumentDB with Custom Images (Required)

The official DocumentDB v0.109-0 images have a bug that breaks LightRAG. You must deploy with custom images from the `hossain-rayhan` fork that include the fix:

```bash
# Create namespace and credentials
kubectl create namespace documentdb-test
kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-credentials
  namespace: documentdb-test
type: Opaque
stringData:
  username: docdbadmin
  password: SecurePassword123!
EOF

# Create image pull secret for the custom images
kubectl create secret docker-registry ghcr-pull-secret \
    -n documentdb-test \
    --docker-server=ghcr.io \
    --docker-username=<your-github-username> \
    --docker-password=<your-github-token>

# Patch default service account to use the pull secret
kubectl patch serviceaccount default -n documentdb-test \
    -p '{"imagePullSecrets": [{"name": "ghcr-pull-secret"}]}'

# Deploy DocumentDB with custom images
kubectl apply -f - <<EOF
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: lightrag-test
  namespace: documentdb-test
spec:
  environment: aks
  nodeCount: 1
  instancesPerNode: 1
  # Custom images with _id lookup fix (v0.112.0+)
  documentDBImage: ghcr.io/hossain-rayhan/documentdb-kubernetes-operator/documentdb:0.112.0
  gatewayImage: ghcr.io/hossain-rayhan/documentdb-kubernetes-operator/gateway:0.112.0
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: LoadBalancer
  sidecarInjectorPluginName: cnpg-i-sidecar-injector.documentdb.io
EOF

# Wait for DocumentDB to be healthy
kubectl wait --for=jsonpath='{.status.phase}'="Cluster in healthy state" \
    documentdb/lightrag-test -n documentdb-test --timeout=300s
```

Verify the custom images are being used:
```bash
kubectl get pod -n documentdb-test -l documentdb.io/name=lightrag-test \
    -o jsonpath='{.items[0].spec.volumes[?(@.name=="documentdb")].image.reference}{"\n"}'
# Expected: ghcr.io/hossain-rayhan/documentdb-kubernetes-operator/documentdb:0.112.0
```

### Test 1: Verify `_id` Lookup Fix

This test confirms the DocumentDB `_id` lookup bug (fixed in v0.110-0+) is resolved:

```bash
python3 -c "
from pymongo import MongoClient

# Update with your DocumentDB connection details
client = MongoClient('mongodb://<user>:<password>@<host>:10260/?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&directConnection=true')
db = client['test_id_lookup']
col = db['test']
col.drop()

# Insert and immediately read back by _id
result = col.insert_one({'name': 'test', 'value': 42})
print(f'Inserted _id: {result.inserted_id}')

# This was failing with 'pruned relation' error in v0.109-0
found = col.find_one({'_id': result.inserted_id})
if found:
    print(f'SUCCESS: _id lookup works! Found: {found}')
else:
    print('FAILED: _id lookup returned None')

col.drop()
client.close()
"
```

**Expected output:**
```
Inserted _id: <ObjectId>
SUCCESS: _id lookup works! Found: {'_id': ObjectId('...'), 'name': 'test', 'value': 42}
```

### Test 2: Document Ingestion

Test that documents can be ingested without "pruned relation" errors:

```bash
# Insert a short document (faster processing)
curl -s -X POST http://localhost:9621/documents/text \
  -H "Content-Type: application/json" \
  -d '{"text": "Microsoft Azure is a cloud computing platform. It competes with AWS and Google Cloud."}' | jq .
```

**Expected output:**
```json
{
  "status": "success",
  "message": "Text successfully received. Processing will continue in background.",
  "track_id": "insert_..."
}
```

Check processing status in logs (wait 2-3 minutes for LLM to complete):
```bash
kubectl logs -n lightrag -l app.kubernetes.io/name=lightrag --tail=20 | grep -E "(Completed|Processing|Extracting)"
```

**Expected:** `INFO: Completed processing file X/X: unknown_source`

### Test 3: RAG Queries

Test different query modes:

```bash
# Naive mode (direct retrieval)
curl -s -X POST http://localhost:9621/query \
  -H "Content-Type: application/json" \
  -d '{"query": "What is AWS?", "mode": "naive"}' | jq .

# Local mode (uses knowledge graph)
curl -s -X POST http://localhost:9621/query \
  -H "Content-Type: application/json" \
  -d '{"query": "What companies compete with AWS?", "mode": "local"}' | jq .

# Hybrid mode (combines local + global)
curl -s -X POST http://localhost:9621/query \
  -H "Content-Type: application/json" \
  -d '{"query": "What is cloud computing?", "mode": "hybrid"}' | jq .
```

**Expected:** Each query should return a `response` with relevant information.

### Test 4: Verify Data in DocumentDB

Verify entities, relations, and documents are stored in DocumentDB:

```bash
python3 -c "
from pymongo import MongoClient

# Update with your DocumentDB connection details
c = MongoClient('mongodb://<user>:<password>@<host>:10260/?authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&directConnection=true')
db = c['lightrag']

print('=== COLLECTIONS ===')
for col in sorted(db.list_collection_names()):
    count = db[col].count_documents({})
    print(f'  {col}: {count} docs')

print('\n=== ENTITIES ===')
for e in db['entity_chunks'].find():
    print(f'  - {e.get(\"_id\", \"?\")}')

print('\n=== RELATIONS ===')
for r in db['relation_chunks'].find():
    print(f'  - {r.get(\"_id\", \"?\")}')

print('\n=== DOC STATUS ===')
for d in db['doc_status'].find({}, {'_id': 1, 'status': 1}):
    status = d.get('status', '?')
    doc_id = str(d.get('_id', '?'))[:40]
    print(f'  - {doc_id}... : {status}')

c.close()
"
```

**Expected output:**
```
=== COLLECTIONS ===
  chunk_entity_relation: X docs
  doc_status: X docs
  entity_chunks: X docs
  full_docs: X docs
  ...

=== ENTITIES ===
  - AWS
  - Microsoft Azure
  - Google Cloud
  ...

=== RELATIONS ===
  - AWS<SEP>Amazon
  ...

=== DOC STATUS ===
  - doc-xxx... : processed
  ...
```

### Test 5: WebUI Verification

1. Open http://localhost:9621 in your browser
2. **Documents tab**: Should show ingested documents with status (Completed/Failed)
3. **Knowledge Graph tab**: Should display entity nodes (AWS, Azure, etc.) with connecting edges
4. **Query tab**: Enter "What is AWS?" and select a mode — should return contextual answer

### Test Summary Checklist

| Test | Command/Action | Expected Result |
|------|---------------|-----------------|
| `_id` lookup | Python pymongo test | "SUCCESS: _id lookup works!" |
| Document ingestion | POST /documents/text | `"status": "success"` |
| No pruned errors | Check logs | No "pruned relation" errors |
| Entity extraction | Check logs | "Completed processing file" |
| RAG query | POST /query | Returns relevant `response` |
| Data persistence | Python collection count | Collections have documents |
| WebUI Documents | Browser | Shows document list with status |
| WebUI Graph | Browser | Shows entity nodes and edges |

### Common Issues

| Issue | Cause | Solution |
|-------|-------|----------|
| "pruned relation" error | DocumentDB < v0.110-0 | Upgrade to v0.110-0+ |
| Document status: Failed | LLM timeout | Try shorter documents; check Ollama resources |
| Empty query response | Document still processing | Wait 2-3 min; check logs for completion |
| Graph shows no edges | LLM output format errors | Normal for some docs; relation extraction is LLM-dependent |

## Known Issues

### "Pruned Relation" Error on `_id` Lookups (Fixed in v0.110-0)

**Status:** Fixed in DocumentDB v0.110-0 ([PR #459](https://github.com/documentdb/documentdb/pull/459))

**Affected versions:** DocumentDB extension ≤ v0.109-0

There is a bug in DocumentDB where queries using the `_id` index fail after any write operation:

```
trying to open a pruned relation, full error: {
  'ok': 0.0, 
  'code': 1, 
  'codeName': 'SqlState(EXX000)', 
  'errmsg': 'trying to open a pruned relation'
}
```

**Root Cause:** In PostgreSQL 18, the custom planner's fast-path read plan did not correctly set `unprunableRelIds`, causing the `_id` index relation to be incorrectly marked as prunable. The fix ([commit e2c5520](https://github.com/documentdb/documentdb/commit/e2c552023f455b3abfa261439e7809f80afeeeec)) properly sets non-prunable RT indexes for PG 18.

**What fails vs. what works:**

| Operation | Result |
|-----------|--------|
| `insert_one({...})` | ✅ Works |
| `find_one({})` (empty filter) | ✅ Works |
| `find_one({'name': value})` | ✅ Works |
| `count_documents({})` | ✅ Works |
| `find_one({'_id': id})` after any write | ❌ **FAILS** |

**Timeline:**
| Event | Date |
|-------|------|
| Bug fix merged to main | Feb 25, 2026 |
| v0.109-0 released (does NOT include fix) | Mar 9, 2026 |
| v0.112.0 (verified fix) | Apr 21, 2026 |

**Resolution:** Upgrade to DocumentDB extension v0.110-0 or later. Tested and verified working with v0.112.0.

**Impact:** This prevents LightRAG document ingestion from working because the ingestion workflow reads back document status immediately after writing it using `find_one({'_id': inserted_id})`.

## Troubleshooting

### LightRAG pod stuck in `Running` but not `Ready`

The most common cause is `createIndex` hanging on DocumentDB. Verify the init container patch applied correctly:

```bash
POD=$(kubectl get pod -l app.kubernetes.io/name=lightrag -n lightrag -o jsonpath='{.items[0].metadata.name}')
kubectl logs -n lightrag "$POD" -c patch-for-documentdb
# Should show: "DocumentDB compatibility patches applied"
```

### Cannot connect to DocumentDB

Verify the gateway service is reachable from the lightrag namespace:

```bash
kubectl run mongo-test --rm -it --restart=Never -n lightrag --image=mongo:7 \
  --command -- mongosh "<your-connection-string>" --eval 'db.adminCommand({ping:1})'
```

### LLM errors during document processing

Check that Ollama has the models pulled:

```bash
OLLAMA_POD=$(kubectl get pod -l app=ollama -n lightrag -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n lightrag "$OLLAMA_POD" -- ollama list
```
