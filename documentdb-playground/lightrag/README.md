# LightRAG with DocumentDB

This playground deploys [LightRAG](https://github.com/HKUDS/LightRAG) — a
graph-based Retrieval-Augmented Generation engine — using DocumentDB as its
MongoDB-compatible storage backend.

## Architecture

```
┌─────────────────────────────────────────────────────┐
│                  Kubernetes Cluster                 │
│                                                     │
│  ┌──────────────┐    ┌──────────────┐               │
│  │   LightRAG   │───▶│   Ollama     │               │
│  │ (RAG Engine) │    │ (LLM + Embed)│               │
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
│  ├─ KV storage      → MongoKVStorage  (DocumentDB)  │
│  ├─ Graph storage   → MongoGraphStorage (DocumentDB)│
│  ├─ Doc status      → MongoDocStatusStorage (DocDB) │
│  └─ Vector storage  → NanoVectorDBStorage  (local)  │
└─────────────────────────────────────────────────────┘
```

LightRAG stores knowledge graph nodes, edges, document metadata, and LLM
response caches in DocumentDB collections. Vector embeddings use local
file-based storage because DocumentDB does not implement the MongoDB Atlas
`$vectorSearch` operator.

## Prerequisites

- A Kubernetes cluster with the [DocumentDB operator](../../README.md) installed
- Helm v3.0+ and kubectl configured for your cluster
- ~10 GiB free memory on a single node (Ollama needs 4–8 GiB for the default
  model, LightRAG needs ~2 GiB)
- Python with `pymongo` if you want to run the verification script in
  [Verification](#verification)

## Quick Start

> **Before applying `documentdb.yaml`**, edit it and replace the placeholder
> password (`ChangeMe!ReplaceBeforeUsing`) with a real one.

```bash
# 1. Deploy a DocumentDB instance (skip if you already have one)
kubectl create namespace documentdb-test
kubectl apply -f documentdb.yaml
kubectl wait --for=jsonpath='{.status.status}'="Cluster in healthy state" \
    documentdb/documentdb-cluster -n documentdb-test --timeout=300s

# 2. Deploy LightRAG + Ollama
./scripts/deploy.sh

# 3. Access the UI
kubectl port-forward svc/lightrag 9621:9621 -n lightrag
# Open http://localhost:9621

# Cleanup when done
./scripts/cleanup.sh
```

`scripts/deploy.sh` defaults to `DOCUMENTDB_NAMESPACE=documentdb-test`,
`DOCUMENTDB_CLUSTER=documentdb-cluster`, and `LIGHTRAG_NAMESPACE=lightrag`.
Override via env vars if your cluster uses different names:

```bash
DOCUMENTDB_NAMESPACE=my-ns DOCUMENTDB_CLUSTER=my-cluster LIGHTRAG_NAMESPACE=rag \
    ./scripts/deploy.sh
```

## What the Scripts Do

`scripts/deploy.sh`:

1. Applies [`helm/ollama.yaml`](helm/ollama.yaml) (Namespace, PVC, Deployment,
   Service) and waits for the pod to become Ready.
2. Pulls the embedding model (`nomic-embed-text`, ~274 MB) and the LLM
   (`qwen2.5:3b`, ~1.9 GB) into the Ollama PVC.
3. Reads the connection string from the `DocumentDB` resource's
   `status.connectionString`, resolves the embedded `kubectl get secret`
   commands, swaps the ClusterIP for the in-cluster DNS name, and strips the
   `replicaSet=rs0` query parameter (incompatible with `directConnection=true`
   when talking directly to the gateway).
4. Installs the `helm/lightrag` chart with the resolved `MONGO_URI`.

## Configuration

### Switching to OpenAI

Edit [`helm/lightrag-values.yaml`](helm/lightrag-values.yaml):

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

OpenAI is much faster than CPU Ollama. You can also drop `LLM_TIMEOUT` and
`MAX_ASYNC` overrides when using a hosted provider.

### Tuning for slow CPU inference

The default values pin a generous LLM timeout because `qwen2.5:3b` on CPU can
take 5–10 minutes per chunk for entity extraction:

```yaml
env:
  LLM_TIMEOUT: "900"        # default 180s — too short for CPU Ollama
  EMBEDDING_TIMEOUT: "120"
  MAX_ASYNC: "2"            # cap concurrent LLM calls per Ollama replica
```

### Storage backends

| Store          | Backend                | Notes                           |
| -------------- | ---------------------- | ------------------------------- |
| KV             | `MongoKVStorage`       | Documents, chunks, entities     |
| Graph          | `MongoGraphStorage`    | Knowledge graph nodes and edges |
| Doc status     | `MongoDocStatusStorage`| Per-document processing state   |
| Vectors        | `NanoVectorDBStorage`  | Local file (PVC) — see below    |

`NanoVectorDBStorage` is used instead of `MongoVectorDBStorage` because
DocumentDB does not implement the MongoDB Atlas `$vectorSearch` aggregation
operator that `MongoVectorDBStorage` requires.

## Expected Startup Warnings

LightRAG logs two non-fatal errors during startup against DocumentDB. They
are safe to ignore:

| Log line                                                         | Why it's harmless                                                                 |
| ---------------------------------------------------------------- | --------------------------------------------------------------------------------- |
| `createIndex.collation is not implemented yet` (on `doc_status`) | DocumentDB doesn't support collation indexes; LightRAG continues without them.    |
| `Pipeline stage name not recognized: $listSearchIndexes`         | Atlas-only API; LightRAG already falls back to regex search and logs as much.     |

`MongoVectorDBStorage` would call Atlas `$vectorSearch`, but we don't use it
(see [Storage backends](#storage-backends)).

## Verification

After `deploy.sh` finishes and you have port-forward running:

```bash
# 1. Health
curl -s http://localhost:9621/health | jq -r .status
# Expected: healthy

# 2. Insert a document
curl -s -X POST http://localhost:9621/documents/text \
  -H "Content-Type: application/json" \
  -d '{"text": "Amazon Web Services (AWS) is a cloud platform. AWS competes with Microsoft Azure and Google Cloud Platform."}' | jq .
# Expected: {"status": "success", "track_id": "..."}

# 3. Wait for processing (5–10 minutes on CPU Ollama)
watch -n 30 'curl -s http://localhost:9621/documents/status_counts | jq .'
# Expected: {"status_counts": {"processed": 1, "all": 1}}
# (macOS users without `watch`: `while sleep 30; do curl -s http://localhost:9621/documents/status_counts | jq .; done`)

# 4. Query
curl -s -X POST http://localhost:9621/query \
  -H "Content-Type: application/json" \
  -d '{"query": "What is AWS?", "mode": "hybrid"}' | jq -r .response
```

Open <http://localhost:9621> for the WebUI:

- **Documents** — ingestion status
- **Knowledge Graph** — extracted entities and relationships
- **Query** — interactive RAG with `naive`, `local`, `global`, `hybrid` modes

### Verify the storage backends

Confirm that KV, graph, and doc-status data are actually living in DocumentDB,
and that vectors are on the local PVC.

```bash
# 1. Check the env LightRAG actually loaded (chart writes to /app/.env, not container env)
kubectl exec -n lightrag deploy/lightrag -- \
    grep -E "LIGHTRAG_(KV|VECTOR|GRAPH|DOC_STATUS)_STORAGE|MONGO_DATABASE" /app/.env

# 2. List collections in DocumentDB — should include full_docs, text_chunks,
#    llm_response_cache (KV), entities/relationships (graph), doc_status.
NS=documentdb-test
CLUSTER=documentdb-cluster
RAW=$(kubectl get documentdb "$CLUSTER" -n "$NS" -o jsonpath='{.status.connectionString}')
URI=$(eval "echo \"$RAW\"" | sed -E 's/[?&]replicaSet=[^&]*//g')
kubectl run mongo-test --rm -it --restart=Never -n lightrag --image=mongo:7 \
    --command -- mongosh "$URI" \
    --eval 'db.getSiblingDB("lightrag").getCollectionNames()'

# 3. Confirm vectors are on the local PVC (NanoVectorDBStorage)
kubectl exec -n lightrag deploy/lightrag -- ls -la /app/data/rag_storage/
# Expected: vdb_chunks.json, vdb_entities.json, vdb_relationships.json
```

## Troubleshooting

### Document stays in `processing` for a long time then fails

The most common cause is the LLM call exceeding `LLM_TIMEOUT`. Check the
LightRAG logs:

```bash
kubectl logs -n lightrag deployment/lightrag --tail=100 | grep -E "(httpx.ReadTimeout|workers initialized)"
```

You should see `LLM func: 2 new workers initialized (Timeouts: Func: 900s, ...)`.
If `Func` is still `180s`, your override didn't apply — re-check
`helm/lightrag-values.yaml` and `helm upgrade`.

### Document fails with "model requires more system memory"

Ollama's qwen2.5:3b needs ~2 GiB of free memory inside the container. The
default Ollama deployment requests 4 GiB and limits to 8 GiB. If your nodes
are tight, scale Ollama down or use a smaller model (`qwen2.5:1.5b`).

### LightRAG pod crashes with `ServerSelectionTimeoutError ... 'rs0' but ... 'None'`

The MongoDB URI contains `replicaSet=rs0` but the gateway exposes itself as a
standalone server. `scripts/deploy.sh` strips this parameter automatically; if
you set `MONGO_URI` manually, make sure to remove `replicaSet=rs0`.

### Cannot connect to DocumentDB

Verify the gateway service is reachable from the lightrag namespace:

```bash
kubectl run mongo-test --rm -it --restart=Never -n lightrag --image=mongo:7 \
  --command -- mongosh "<your-connection-string>" --eval 'db.adminCommand({ping:1})'
```

### Ollama model is missing after a pod restart

Models live on the `ollama-models` PVC defined in `helm/ollama.yaml`. If your
StorageClass deleted the PV (`reclaimPolicy: Delete`), re-pull manually:

```bash
OLLAMA_POD=$(kubectl get pod -l app=ollama -n lightrag -o jsonpath='{.items[0].metadata.name}')
kubectl exec -n lightrag "$OLLAMA_POD" -- ollama pull nomic-embed-text
kubectl exec -n lightrag "$OLLAMA_POD" -- ollama pull qwen2.5:3b
```

## Version Compatibility

| Component           | Tested version                |
| ------------------- | ----------------------------- |
| DocumentDB operator | 0.2.0                         |
| DocumentDB images   | 0.110.0+ (required for `_id` lookup fix) |
| LightRAG            | `ghcr.io/hkuds/lightrag:latest` (core 1.4.x) |
| Ollama              | `ollama/ollama:latest`        |
| Kubernetes          | 1.30+                         |

_Last verified: April 2026._

The `helm/lightrag` chart is intentionally local-only — it is a playground
sample, not a published or supported product chart.
