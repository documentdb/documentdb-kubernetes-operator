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
CONNECTION_STRING=$(eval echo "$(kubectl get documentdb <cluster-name> -n <documentdb-namespace> -o jsonpath='{.status.connectionString}')")
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

The following LightRAG operations have been tested with DocumentDB:

- ✅ Document ingestion and chunking
- ✅ Entity and relationship extraction (via LLM)
- ✅ Knowledge graph storage and traversal
- ✅ LLM response caching
- ✅ Naive, local, global, and hybrid RAG queries
- ✅ Document status tracking
- ✅ WebUI for graph visualization

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
