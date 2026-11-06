# Bug Bash: DocumentDB Kubernetes Operator on Azure Local

This guide is a step-by-step test plan for running DocumentDB on **AKS enabled by Azure Arc on Azure Local** with the DocumentDB Kubernetes Operator. It's written so that an engineer, or an AI agent working on their behalf, can follow it without prior knowledge of the project.

Every command shows its expected result. Record **PASS**, **FAIL** or **BLOCKED** for each test case ID in the [results table](#7-results-and-reporting).

---

## 1. Scope at a glance

### What you're testing

| Component | Version |
|---|---|
| DocumentDB Kubernetes Operator (Helm chart) | `1.1.0` (`oci://ghcr.io/documentdb/documentdb-operator`) |
| DocumentDB engine (PostgreSQL extension and gateway) | `1.0.0` (extension schema `1.0-0`) |
| PostgreSQL | 18.x |
| Platform | AKS on Azure Local, Kubernetes 1.33.x, Arc-connected |

### Why Azure Local runs in "combined image" mode
The operator normally mounts the DocumentDB extension into PostgreSQL pods with the Kubernetes **ImageVolume** feature. That needs containerd 2.1 or later on the nodes. AKS Arc nodes currently run **containerd 1.7**, so the operator detects this and automatically uses a **combined image**, with PostgreSQL 18 and DocumentDB in one container image: `ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb-combined:18-1.0.0`. You don't configure anything for this; part of this bug bash checks that it happens.

### In scope (tested)

| Area | Test IDs |
|---|---|
| Install the operator and its dependencies | SETUP-01 … SETUP-05 |
| Create a DocumentDB cluster; pods and services come up; combined mode selected | DEP-01 … DEP-05 |
| Connectivity (in-cluster client and laptop port-forward) | CON-01 … CON-02 |
| CRUD and BSON document handling | DATA-01, DATA-02 |
| Regular-expression queries | DATA-03 |
| Decimal128 | DATA-04 |
| Geospatial queries (`2dsphere`, `$near`, `$geoWithin`) | DATA-05 |
| Vector search (HNSW, cosine) | DATA-06 |
| Aggregation and secondary indexes, unique indexes, text indexes | DATA-07 … DATA-09 |
| Multi-document transactions | DATA-10 |
| Authentication and TLS | AUTH-01 … AUTH-07 |
| Admission validation (invalid specs rejected) | VAL-01, VAL-02 |
| High availability: scale out, replication, failover, scale in | HA-01 … HA-05 |
| Resilience: pod and operator restarts with data intact | RES-01, RES-02 |
| Logical backup and restore (`mongodump`/`mongorestore`) | BAK-01 … BAK-03 |
| Monitoring (OpenTelemetry sidecar, Prometheus metric) | MON-01, MON-02 |
| Cluster deletion keeps the data volume (`Retain`) | LIFE-01 |

### Out of scope (not tested) and why

| Area | Why it isn't tested on Azure Local |
|---|---|
| `Backup` / `ScheduledBackup` resources (volume-snapshot backups) | They need a CSI driver with **volume snapshots**. The AKS Arc disk driver (`disk.csi.akshci.com`) doesn't support snapshots yet. Use logical backups (BAK-*) instead. |
| Restore from a `Backup` | Depends on the snapshot backups above. |
| **Deleted-cluster recovery** (`bootstrap.recovery.persistentVolume`) | Recovering from a retained volume needs CSI **volume cloning**, which the AKS Arc disk driver doesn't support. LIFE-01 only checks that the volume is *retained*. |
| `LoadBalancer` service exposure / external client access | These clusters have no load-balancer provider, so `LoadBalancer` services get no external IP. Use port-forward (CON-02). |
| Multi-region / cross-cluster replication and promotion | Needs multiple connected clusters and fleet networking; not part of this bash. |
| Operator upgrade from 1.0.0 | Operator 1.0.0 can't run DocumentDB on containerd 1.7, so no 1.0.0 cluster can exist on Azure Local. |
| DocumentDB engine upgrade (`spec.documentDBVersion`) | No engine version newer than 1.0.0 has been released. |
| ImageVolume mode | Not available on containerd 1.7 (expected; see DEP-03). |
| Change streams (preview feature gate) | Preview feature with no combined image. |
| Creating extra database users (`createUser`) | The operator-managed user has `readWriteAnyDatabase` and `clusterAdmin` but not user administration, so `createUser` returns `Unauthorized` (expected). |
| arm64 nodes | The bug bash clusters are amd64 only. |
| Formal performance and scale benchmarking | Out of scope; note anything that looks unusually slow. |

---

## 2. Rules for everyone (humans and AI agents)

1. **Work only in your own namespaces:** `bb-<alias>` (DocumentDB) and `bb-<alias>-client` if needed. Replace `<alias>` with your short name, in lowercase letters and digits only.
2. **Don't modify platform components:** `kube-system`, `azure-arc`, `mdc`, `gatekeeper-system`, the `default` StorageClass, the CSI driver, node settings, or Azure extensions.
3. **Install the operator only once per cluster.** One person (the *cluster owner*, see [section 3](#3-cluster-assignments)) runs SETUP-03 … SETUP-05. Everyone else checks SETUP-05 and continues from DEP-01.
4. **Never put real secrets in bug reports.** The passwords in this guide are throwaway examples.
5. **Run commands one at a time** and compare against the *Expected* result before moving on. If a step fails, collect diagnostics ([section 8](#8-collecting-diagnostics)), file the bug, and continue with the next independent test where possible.
6. **AI agents:** treat each test ID as a separate task. Substitute the variables from [section 4](#4-variables), run the commands, compare the output with *Expected*, and record PASS/FAIL with the evidence (the command output). Don't skip *Expected* checks or invent results.

---

## 3. Cluster assignments

The organizer fills in this table. Each cluster has one **cluster owner**, who installs the operator. Everyone runs the **core path** (SETUP-05, DEP, CON, DATA, AUTH, VAL), plus their cluster's **deep-dive** areas.

| Cluster | Azure Local location | Cluster owner | Engineers | Deep-dive areas |
|---|---|---|---|---|
| `<CLUSTER_1>` | `<LOCATION>` | `<name>` | `<names>` | HA, RES |
| `<CLUSTER_2>` | `<LOCATION>` | `<name>` | `<names>` | BAK, LIFE |
| `<CLUSTER_3>` | `<LOCATION>` | `<name>` | `<names>` | MON, AUTH (extended) |
| `<CLUSTER_4>` | `<LOCATION>` | `<name>` | `<names>` | HA (3 instances), DATA (larger data sets) |
| `<CLUSTER_5>` | `<LOCATION>` | `<name>` | `<names>` | Exploratory (your own scenarios) |

---

## 4. Variables

Set these in every terminal. Example values are shown; replace the `<...>` values.

```bash
export TENANT="<TENANT_DOMAIN_OR_ID>"            # tenant that hosts the clusters
export SUBSCRIPTION="<SUBSCRIPTION_ID>"
export RESOURCE_GROUP="<RESOURCE_GROUP>"
export CLUSTER="<CLUSTER_NAME>"                  # your assigned cluster
export ALIAS="<alias>"                           # e.g. jdoe
export NS="bb-${ALIAS}"                          # your DocumentDB namespace
export DB="ddb-${ALIAS}"                         # your DocumentDB cluster name
export DB_USER="bb_user"
export DB_PASS="BugBash-$(openssl rand -hex 6)"  # throwaway; letters, digits and '-'
export KUBECONFIG="$HOME/.kube/${CLUSTER}.yaml"  # written by the Arc proxy in SETUP-02
export OPERATOR_VERSION="1.1.0"
echo "Your DocumentDB password for this session: ${DB_PASS}"
```

Keep `DB_PASS` for the whole session. If you open a new terminal, set it to the same value.

---

## 5. Setup

### SETUP-01: Tools on your machine
Install:
- **Azure CLI** with the `connectedk8s` extension;
- **kubectl** 1.32 or later;
- **Helm** 3.8 or later;
- `jq` and `openssl`.

```bash
az extension add --name connectedk8s --upgrade --yes
az version --query '"azure-cli"' -o tsv
kubectl version --client
helm version --short
```
**Expected:** every command prints a version; no errors.

### SETUP-02: Connect to the cluster through Azure Arc
The clusters' API servers are on a private network, so you reach them through **Arc cluster connect**.

```bash
az login --tenant "$TENANT"
az account set --subscription "$SUBSCRIPTION"
```

In a **separate terminal**, start the proxy and leave it running:
```bash
az connectedk8s proxy -n "$CLUSTER" -g "$RESOURCE_GROUP" \
  --file "$HOME/.kube/${CLUSTER}.yaml" --port 47011
```

Back in your working terminal (with the variables from section 4 set):
```bash
kubectl get nodes -o custom-columns='NAME:.metadata.name,K8S:.status.nodeInfo.kubeletVersion,RUNTIME:.status.nodeInfo.containerRuntimeVersion,ARCH:.status.nodeInfo.architecture'
```
**Expected:** 4 nodes, Kubernetes `v1.33.x`, runtime `containerd://1.7.x`, architecture `amd64`.

> If kubectl later fails with `Unknown proxy error occurred` or connection errors, restart the proxy command. Arc cluster connect drops occasionally, and its credential refresh can fail on long sessions. That's not a DocumentDB bug.

### SETUP-03: cert-manager (cluster owner only)
```bash
kubectl get crd certificates.cert-manager.io >/dev/null 2>&1 && echo "cert-manager already installed" || {
  helm repo add jetstack https://charts.jetstack.io --force-update
  helm install cert-manager jetstack/cert-manager -n cert-manager --create-namespace \
    --set crds.enabled=true --wait --timeout 10m
}
kubectl get pods -n cert-manager
```
**Expected:** 3 pods `Running` (`cert-manager`, `cert-manager-cainjector`, `cert-manager-webhook`).

### SETUP-04: Install the DocumentDB operator (cluster owner only)
```bash
helm install documentdb-operator oci://ghcr.io/documentdb/documentdb-operator \
  --version "$OPERATOR_VERSION" \
  --namespace documentdb-operator --create-namespace \
  --wait --timeout 10m
```
**Expected:** `STATUS: deployed`.

> If the operator is already installed, run `helm list -n documentdb-operator` and confirm the chart is `documentdb-operator-1.1.0` instead.

### SETUP-05: Verify the operator (everyone)
```bash
helm list -n documentdb-operator
kubectl get pods -n documentdb-operator
kubectl get pods -n cnpg-system
kubectl get deploy documentdb-operator -n documentdb-operator -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl get deploy sidecar-injector -n cnpg-system -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl get crd | grep documentdb.io
```
**Expected:**
- chart `documentdb-operator-1.1.0`, status `deployed`;
- pods `documentdb-operator-…`, `documentdb-operator-cloudnative-pg-…` and `sidecar-injector-…` are `Running` with `READY 1/1`;
- images `ghcr.io/documentdb/documentdb-kubernetes-operator/operator:1.1.0` and `…/sidecar:1.1.0`;
- CRDs `dbs.documentdb.io`, `backups.documentdb.io` and `scheduledbackups.documentdb.io` exist.

---

## 6. Test cases

### 6.1 Deployment (DEP)

#### DEP-01: Create a DocumentDB cluster
```bash
kubectl create namespace "$NS"
kubectl -n "$NS" create secret generic documentdb-credentials \
  --from-literal=username="$DB_USER" --from-literal=password="$DB_PASS"

cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata:
  name: ${DB}
  namespace: ${NS}
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource:
    storage:
      pvcSize: 10Gi
  exposeViaService:
    serviceType: ClusterIP
EOF

time kubectl -n "$NS" wait "documentdb/${DB}" \
  --for=jsonpath='{.status.status}'='Cluster in healthy state' --timeout=15m
```
**Expected:** `condition met`, usually within **1–3 minutes** (longer the first time a node pulls the ~700 MB image). **Record the time.**

#### DEP-02: Pods, services and storage
```bash
kubectl -n "$NS" get documentdb,pods,svc,pvc
```
**Expected:**
- the DocumentDB shows `Cluster in healthy state`;
- pod `${DB}-1` is `Running` with `READY 2/2` (containers `postgres` and `documentdb-gateway`);
- service `documentdb-service-${DB}` is `ClusterIP` on port `10260`;
- PVC `${DB}-1` is `Bound` on StorageClass `default`, 10Gi.

#### DEP-03: Combined image mode was selected
```bash
kubectl -n "$NS" get events --field-selector "involvedObject.name=${DB},reason=ImageModeSelected" -o jsonpath='{.items[0].message}{"\n"}'
kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='mode={.metadata.annotations.documentdb\.io/image-mode} image={.spec.imageName}{"\n"}'
kubectl -n "$NS" get pod "${DB}-1" -o jsonpath='postgres={.spec.containers[?(@.name=="postgres")].image} imageVolumes=[{.spec.volumes[*].image.reference}]{"\n"}'
```
**Expected:**
- the event says `Using combined image mode (image …documentdb-combined:18-1.0.0): node "…" runs container runtime "containerd://1.7…"; image volumes need containerd >= 2.1 or CRI-O >= 1.33`;
- `mode=combined`, `image=ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb-combined:18-1.0.0`;
- the `postgres` container uses that image, and `imageVolumes=[]` (empty).

> If your cluster's nodes run containerd 2.1 or later, the operator selects `imageVolume` instead. That's correct behavior, but note it in your results, because some expected values differ.

#### DEP-04: Status, versions and extension
```bash
kubectl -n "$NS" get documentdb "$DB" -o jsonpath='status={.status.status}{"\n"}schemaVersion={.status.schemaVersion}{"\n"}documentDBImage={.status.documentDBImage}{"\n"}gatewayImage={.status.gatewayImage}{"\n"}'
kubectl -n "$NS" exec "${DB}-1" -c postgres -- psql -U postgres -tAc "select version()"
kubectl -n "$NS" exec "${DB}-1" -c postgres -- psql -U postgres -tAc "select extversion from pg_extension where extname='documentdb'"
```
**Expected:** `schemaVersion=1.0.0`, `documentDBImage=…documentdb-combined:18-1.0.0`, `gatewayImage=…/gateway:1.0.0`, `PostgreSQL 18.x`, extension `1.0-0`.

#### DEP-05: Connection string in status
```bash
kubectl -n "$NS" get documentdb "$DB" -o jsonpath='{.status.connectionString}{"\n"}'
```
**Expected:** a `mongodb://…@<cluster-ip>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true…` string. Its embedded `$(kubectl get secret …)` expressions read your credentials secret.

### 6.2 Connectivity (CON)

#### CON-01: In-cluster MongoDB shell client
Most tests below use this client pod, so they don't depend on your laptop's network.
```bash
kubectl -n "$NS" run mongosh --image=mongo:8.0 --restart=Never --command -- sleep 28800
kubectl -n "$NS" wait --for=condition=Ready pod/mongosh --timeout=5m
export URI="mongodb://${DB_USER}:${DB_PASS}@documentdb-service-${DB}:10260/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256"
m() { kubectl -n "$NS" exec mongosh -- mongosh --quiet "$URI" --eval "$1"; }
m 'db.runCommand({ping:1})'
```
**Expected:** `{ ok: 1 }`.

> `tlsAllowInvalidCertificates=true` is needed because the default gateway certificate is self-signed (see AUTH-06). `m '<javascript>'` is a helper used throughout the guide; define it again in every new terminal.

#### CON-02: From your laptop through port-forward (optional; needs `mongosh` installed locally)
```bash
kubectl -n "$NS" port-forward "svc/documentdb-service-${DB}" 10260:10260
# in another terminal:
mongosh "mongodb://${DB_USER}:${DB_PASS}@127.0.0.1:10260/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256&directConnection=true" --eval 'db.runCommand({ping:1})'
```
**Expected:** `{ ok: 1 }`. MongoDB tools such as Compass work the same way through the port-forward.

### 6.3 Database functionality (DATA)

Run each snippet with `m '<snippet>'`, or run all of them at once with the script in [Appendix A](#appendix-a-full-data-validation-script). Each snippet prints its own result.

#### DATA-01: CRUD
```javascript
const c=db.getSiblingDB("bugbash").crud; c.drop();
c.insertOne({_id:1,name:"alice",qty:5});
c.insertMany([{_id:2,name:"bob",qty:3},{_id:3,name:"carol",qty:8}]);
c.updateOne({_id:2},{$inc:{qty:10},$set:{tag:"updated"}});
c.replaceOne({_id:3},{name:"carol2",qty:1});
c.deleteOne({_id:1});
c.updateOne({_id:9},{$set:{qty:0}},{upsert:true});
printjson(c.find().sort({_id:1}).toArray());
```
**Expected:** three documents. `_id:2` has `qty:13` and `tag:"updated"`, `_id:3` is `{name:"carol2",qty:1}`, and `_id:9` (upserted) has `qty:0`. `_id:1` is gone.

#### DATA-02: BSON types and document parsing
```javascript
const c=db.getSiblingDB("bugbash").types; c.drop(); const oid=new ObjectId();
c.insertOne({_id:oid,i32:NumberInt(7),i64:NumberLong("9007199254740993"),dbl:1.5,str:"héllo ✓",bool:true,nul:null,
  date:new Date("2026-01-02T03:04:05Z"),bin:BinData(0,"AQIDBA=="),arr:[1,"two",{three:3}],nested:{a:{b:{c:[1,2,3]}}},ts:Timestamp({t:1700000000,i:1})});
printjson(c.findOne({_id:oid}));
print("typed query matches: "+c.countDocuments({i32:{$type:"int"},i64:{$type:"long"},bin:{$type:"binData"}}));
```
**Expected:** every field round-trips with its type: `Long('9007199254740993')` (no precision loss), the date as `ISODate("2026-01-02T03:04:05.000Z")`, the UTF-8 string unchanged, `null` kept, and nested arrays and objects intact. `typed query matches: 1`.

#### DATA-03: Regular expressions
```javascript
const c=db.getSiblingDB("bugbash").regex; c.drop();
c.insertMany([{s:"Apple pie"},{s:"apple tart"},{s:"Banana"},{s:"pineapple"}]);
print(c.countDocuments({s:/^apple/i}), c.countDocuments({s:{$regex:"apple$"}}), c.countDocuments({s:{$not:/apple/i}}));
```
**Expected:** `2 1 1`.

#### DATA-04: Decimal128
```javascript
const c=db.getSiblingDB("bugbash").money; c.drop();
c.insertMany([{amt:NumberDecimal("0.1")},{amt:NumberDecimal("0.2")},{amt:NumberDecimal("12345678901234567890.123456789")}]);
printjson(c.aggregate([{$match:{amt:{$lt:NumberDecimal("1")}}},{$group:{_id:null,total:{$sum:"$amt"}}}]).toArray());
printjson(c.findOne({amt:{$gt:NumberDecimal("1e18")}}).amt);
```
**Expected:** `total: Decimal128('0.3')` (exact, not `0.30000000000000004`) and `Decimal128('12345678901234567890.123456789')` (all digits kept).

#### DATA-05: Geospatial
```javascript
const c=db.getSiblingDB("bugbash").places; c.drop();
c.insertMany([{name:"Seattle",loc:{type:"Point",coordinates:[-122.3321,47.6062]}},
              {name:"Redmond",loc:{type:"Point",coordinates:[-122.1215,47.6740]}},
              {name:"Portland",loc:{type:"Point",coordinates:[-122.6765,45.5231]}}]);
c.createIndex({loc:"2dsphere"});
print(c.find({loc:{$near:{$geometry:{type:"Point",coordinates:[-122.3321,47.6062]},$maxDistance:25000}}}).toArray().map(x=>x.name));
print(c.countDocuments({loc:{$geoWithin:{$centerSphere:[[-122.3321,47.6062],50/6378.1]}}}));
```
**Expected:** `[ 'Seattle', 'Redmond' ]` (nearest first) and `2`.

#### DATA-06: Vector search
```javascript
const d=db.getSiblingDB("bugbash"); d.vectors.drop();
d.vectors.insertMany([{_id:"a",v:[1.0,0.0,0.0]},{_id:"b",v:[0.9,0.1,0.0]},{_id:"c",v:[0.0,1.0,0.0]},{_id:"d",v:[0.0,0.0,1.0]}]);
printjson(d.runCommand({createIndexes:"vectors",indexes:[{name:"v_hnsw",key:{v:"cosmosSearch"},
  cosmosSearchOptions:{kind:"vector-hnsw",m:16,efConstruction:64,similarity:"COS",dimensions:3}}]}));
printjson(d.vectors.aggregate([{$search:{cosmosSearch:{vector:[1.0,0.05,0.0],path:"v",k:2}}},{$project:{_id:1}}]).toArray());
```
**Expected:** `createIndexes` returns `ok: 1`, and the search returns `a` and `b`, the two vectors closest to the query.

#### DATA-07: Aggregation and secondary index
```javascript
const c=db.getSiblingDB("bugbash").orders; c.drop();
c.insertMany([...Array(1000).keys()].map(i=>({cust:"c"+(i%10),qty:i%7,when:new Date(1700000000000+i*60000)})));
c.createIndex({cust:1,when:-1});
printjson(c.aggregate([{$group:{_id:"$cust",total:{$sum:"$qty"}}},{$sort:{total:-1,_id:1}},{$limit:3}]).toArray());
print("uses index: "+/cust_1_when_-1/.test(JSON.stringify(c.find({cust:"c1"}).sort({when:-1}).explain())));
```
**Expected:** top 3 are `c3:303`, `c6:302`, `c2:301`; `uses index: true`.

#### DATA-08: Unique index
```javascript
const c=db.getSiblingDB("bugbash").users; c.drop(); c.createIndex({email:1},{unique:true}); c.insertOne({email:"a@x.com"});
try { c.insertOne({email:"a@x.com"}); print("UNEXPECTED: duplicate accepted"); } catch(e) { print("rejected code="+e.code); }
```
**Expected:** `rejected code=11000`.

#### DATA-09: Text index
```javascript
const c=db.getSiblingDB("bugbash").articles; c.drop();
c.insertMany([{t:"fast database on kubernetes"},{t:"slow cooking recipes"}]); c.createIndex({t:"text"});
print(c.countDocuments({$text:{$search:"kubernetes"}}));
```
**Expected:** `1`.

#### DATA-10: Multi-document transaction
```javascript
const d=db.getSiblingDB("bugbash"); d.tx.drop(); d.tx.insertOne({_id:"acct1",bal:100});
const s=db.getMongo().startSession(); const c=s.getDatabase("bugbash").tx;
s.startTransaction(); c.updateOne({_id:"acct1"},{$inc:{bal:-30}}); c.insertOne({_id:"acct2",bal:30}); s.abortTransaction();
print("after abort: docs="+d.tx.countDocuments()+" bal1="+d.tx.findOne({_id:"acct1"}).bal);
s.startTransaction(); c.updateOne({_id:"acct1"},{$inc:{bal:-30}}); c.insertOne({_id:"acct2",bal:30}); s.commitTransaction();
print("after commit: docs="+d.tx.countDocuments()+" bal1="+d.tx.findOne({_id:"acct1"}).bal);
```
**Expected:** `after abort: docs=1 bal1=100`, then `after commit: docs=2 bal1=70`.

### 6.4 Authentication and TLS (AUTH)

Use `tryuri '<uri>'`, which attempts a connection with any URI and prints the first line of the result:
```bash
tryuri() { kubectl -n "$NS" exec mongosh -- mongosh --quiet "$1" --eval "${2:-db.runCommand({ping:1}).ok}" 2>&1 | grep -v '^\s*at ' | head -1; }
H="documentdb-service-${DB}:10260"; Q="tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256&serverSelectionTimeoutMS=8000"
```

| ID | Command | Expected |
|---|---|---|
| AUTH-01 Valid credentials | `tryuri "mongodb://${DB_USER}:${DB_PASS}@${H}/?${Q}"` | `1` |
| AUTH-02 Wrong password | `tryuri "mongodb://${DB_USER}:WrongPass999@${H}/?${Q}"` | `MongoServerError: Invalid key` (rejected) |
| AUTH-03 Unknown user | `tryuri "mongodb://nobody:Whatever123@${H}/?${Q}"` | `MongoServerError: Invalid account: User details not found in the database` |
| AUTH-04 No credentials | `tryuri "mongodb://${H}/?tls=true&tlsAllowInvalidCertificates=true&serverSelectionTimeoutMS=8000" 'db.getSiblingDB("bugbash").crud.findOne()'` | `MongoServerError: Command find is not allowed as the connection is not authenticated yet.` |
| AUTH-05 TLS is required | `tryuri "mongodb://${DB_USER}:${DB_PASS}@${H}/?tls=false&authMechanism=SCRAM-SHA-256&serverSelectionTimeoutMS=8000"` | Connection fails (for example `ECONNRESET`); plaintext isn't accepted |
| AUTH-06 Self-signed certificate | `tryuri "mongodb://${DB_USER}:${DB_PASS}@${H}/?tls=true&authMechanism=SCRAM-SHA-256&serverSelectionTimeoutMS=8000"` | `MongoNetworkError: self-signed certificate…` (expected with the default certificate) |
| AUTH-07 Roles of the operator-managed user | `m 'printjson(db.runCommand({connectionStatus:1}).authInfo.authenticatedUserRoles)'` | `readWriteAnyDatabase` and `clusterAdmin` on `admin` |

> **Known behavior (not a bug):** `db.getSiblingDB("admin").runCommand({createUser:…})` returns `Unauthorized` with the operator-managed user. Extra users can't be created this way in this release.

### 6.5 Admission validation (VAL)

| ID | Command | Expected |
|---|---|---|
| VAL-01 | `kubectl -n "$NS" patch documentdb "$DB" --type merge -p '{"spec":{"nodeCount":0}}' --dry-run=server` | Rejected: `spec.nodeCount … should be greater than or equal to 1` |
| VAL-02 | `kubectl -n "$NS" patch documentdb "$DB" --type merge -p '{"spec":{"schemaVersion":"9.9.9"}}' --dry-run=server` | Rejected by the webhook: `cannot set an explicit schemaVersion without also setting spec.documentDBVersion or spec.documentDBImage…` |

### 6.6 High availability (HA)

#### HA-01: Start a continuous writer
It writes one document every 0.5 s for 20 minutes, and records any failure. Keep it running during HA-02 … HA-04 and RES-02.
```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Pod
metadata: {name: writer, namespace: ${NS}}
spec:
  restartPolicy: Never
  containers:
  - name: writer
    image: mongo:8.0
    command: ["mongosh","--quiet","mongodb://${DB_USER}:${DB_PASS}@documentdb-service-${DB}:10260/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256&serverSelectionTimeoutMS=5000","--eval"]
    args:
    - |
      const c=db.getSiblingDB("ha").ticks; c.drop();
      const end=Date.now()+20*60*1000; let seq=0, ok=0, fail=0;
      while (Date.now()<end) { seq++;
        try { c.insertOne({_id:seq, t:new Date()}); ok++; } catch(e) { fail++; print("FAIL seq="+seq+" "+new Date().toISOString()+" "+e.message); }
        if (seq%120==0) print(new Date().toISOString()+" seq="+seq+" ok="+ok+" fail="+fail);
        sleep(500); }
      print("DONE seq="+seq+" ok="+ok+" fail="+fail+" stored="+c.countDocuments());
EOF
kubectl -n "$NS" wait --for=condition=Ready pod/writer --timeout=5m
```
**Expected:** the pod is `Running`. `kubectl -n "$NS" logs writer` prints a progress line every minute with `fail=0`.

#### HA-02: Scale out to 3 instances
```bash
kubectl -n "$NS" patch documentdb "$DB" --type merge -p '{"spec":{"instancesPerNode":3}}'
kubectl -n "$NS" wait "clusters.postgresql.cnpg.io/${DB}" --for=jsonpath='{.status.readyInstances}'=3 --timeout=15m
kubectl -n "$NS" get pods -l "cnpg.io/cluster=${DB}" -o custom-columns='NAME:.metadata.name,ROLE:.metadata.labels.cnpg\.io/instanceRole,NODE:.spec.nodeName,IMAGE:.spec.containers[0].image'
```
**Expected:** 3 pods, 1 `primary` and 2 `replica`, ideally on different nodes, all using the combined image. **Record the time to 3 ready instances.**

#### HA-03: Replication is healthy
```bash
P=$(kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='{.status.currentPrimary}')
kubectl -n "$NS" exec "$P" -c postgres -- psql -U postgres -tAc "select application_name, state, replay_lag from pg_stat_replication"
```
**Expected:** 2 rows in state `streaming` with sub-second `replay_lag`.

#### HA-04: Failover (delete the primary)
```bash
OLD=$(kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='{.status.currentPrimary}'); date -u; echo "old primary: $OLD"
kubectl -n "$NS" delete pod "$OLD" --wait=false
sleep 60
kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='new primary={.status.currentPrimary} ready={.status.readyInstances}{"\n"}'
kubectl -n "$NS" get svc "documentdb-service-${DB}" -o jsonpath='{.spec.selector}{"\n"}'
kubectl -n "$NS" logs writer | tail -5
```
**Expected:**
- a different pod becomes primary within about **1 minute**, and the old one rejoins as a replica (`ready=3`);
- the service selector includes `cnpg.io/instanceRole: primary`;
- writes resume.

**Record** the failover time and any `FAIL` lines; a few seconds to tens of seconds of failed or paused writes during failover is normal.

#### HA-05: Scale back in and check data
```bash
kubectl -n "$NS" patch documentdb "$DB" --type merge -p '{"spec":{"instancesPerNode":1}}'
kubectl -n "$NS" wait "clusters.postgresql.cnpg.io/${DB}" --for=jsonpath='{.status.readyInstances}'=1 --timeout=15m
m 'const c=db.getSiblingDB("ha").ticks; const n=c.countDocuments(), max=c.find().sort({_id:-1}).limit(1).toArray()[0]._id; print("stored="+n+" maxSeq="+max+" missing="+(max-n))'
```
**Expected:** 1 instance, and `missing=0`. Every acknowledged write is still there; IDs that the writer logged as `FAIL` were never acknowledged, so they don't count.

### 6.7 Resilience (RES)

#### RES-01: PostgreSQL pod restart keeps data
```bash
m 'print(db.getSiblingDB("bugbash").orders.countDocuments())'
P=$(kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='{.status.currentPrimary}')
kubectl -n "$NS" delete pod "$P"
kubectl -n "$NS" wait "documentdb/${DB}" --for=jsonpath='{.status.status}'='Cluster in healthy state' --timeout=10m
kubectl -n "$NS" wait --for=condition=Ready "pod/$P" --timeout=10m
m 'print(db.getSiblingDB("bugbash").orders.countDocuments())'
```
**Expected:** the same count before and after (`1000`), and the cluster healthy again.

#### RES-02: Operator restart doesn't disturb the database (cluster owner coordinates)
```bash
kubectl -n "$NS" get pod "${DB}-1" -o jsonpath='uid={.metadata.uid} restarts={.status.containerStatuses[*].restartCount}{"\n"}'
kubectl -n documentdb-operator rollout restart deploy/documentdb-operator
kubectl -n documentdb-operator rollout status deploy/documentdb-operator --timeout=5m
sleep 90
kubectl -n "$NS" get pod "${DB}-1" -o jsonpath='uid={.metadata.uid} restarts={.status.containerStatuses[*].restartCount}{"\n"}'
kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='mode={.metadata.annotations.documentdb\.io/image-mode}{"\n"}'
```
**Expected:** the same pod UID and restart counts before and after, and `mode=combined` unchanged.

### 6.8 Logical backup and restore (BAK)

#### BAK-01: Dump
```bash
kubectl -n "$NS" exec mongosh -- mongodump --uri="$URI" --tlsInsecure --gzip --archive=/tmp/bugbash.archive.gz
kubectl -n "$NS" exec mongosh -- ls -la /tmp/bugbash.archive.gz
kubectl -n "$NS" cp mongosh:/tmp/bugbash.archive.gz ./bugbash.archive.gz    # keep a copy off the cluster
```
**Expected:** `done dumping …` lines for each collection, with no errors, and a non-empty archive.

#### BAK-02: Restore into a new cluster
```bash
cat <<EOF | kubectl apply -f -
apiVersion: documentdb.io/preview
kind: DocumentDB
metadata: {name: ${DB}-restore, namespace: ${NS}}
spec:
  nodeCount: 1
  instancesPerNode: 1
  documentDbCredentialSecret: documentdb-credentials
  resource: {storage: {pvcSize: 10Gi}}
  exposeViaService: {serviceType: ClusterIP}
EOF
kubectl -n "$NS" wait "documentdb/${DB}-restore" --for=jsonpath='{.status.status}'='Cluster in healthy state' --timeout=15m
export URI2="mongodb://${DB_USER}:${DB_PASS}@documentdb-service-${DB}-restore:10260/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256"
kubectl -n "$NS" exec mongosh -- mongorestore --uri="$URI2" --tlsInsecure --gzip --archive=/tmp/bugbash.archive.gz
```
**Expected:** `N document(s) restored successfully. 0 document(s) failed to restore.`

#### BAK-03: Compare source and restored data
```bash
SUM='const o={}; for (const d of ["bugbash","ha"]) { const s=db.getSiblingDB(d); for (const c of s.getCollectionNames().sort()) { const col=s.getCollection(c); o[d+"."+c]=col.countDocuments()+" idx="+col.getIndexes().map(i=>i.name).sort().join("|"); } } print(JSON.stringify(o));'
diff <(m "$SUM") <(kubectl -n "$NS" exec mongosh -- mongosh --quiet "$URI2" --eval "$SUM") && echo IDENTICAL
```
**Expected:** `IDENTICAL`. Document counts and index names match for every collection, including the vector, 2dsphere, unique and text indexes. If the writer was still running during BAK-01, `ha.ticks` may differ; compare after it finishes.

### 6.9 Monitoring (MON)

#### MON-01: Enable the OpenTelemetry sidecar with a Prometheus endpoint
```bash
kubectl -n "$NS" patch documentdb "$DB" --type merge -p '{"spec":{"monitoring":{"enabled":true,"exporter":{"prometheus":{"port":9188}}}}}'
sleep 60
kubectl -n "$NS" wait "documentdb/${DB}" --for=jsonpath='{.status.status}'='Cluster in healthy state' --timeout=15m
kubectl -n "$NS" get pod "${DB}-1" -o jsonpath='{range .spec.containers[*]}{.name}{" "}{end}{"\n"}{.metadata.annotations.prometheus\.io/scrape} {.metadata.annotations.prometheus\.io/port}{"\n"}'
```
**Expected:** containers `postgres documentdb-gateway otel-collector`; annotations `true 9188`. The pod restarts once to add the sidecar; that's expected.

#### MON-02: Scrape the metric
```bash
kubectl -n "$NS" port-forward "pod/${DB}-1" 19188:9188 &
sleep 5; curl -s localhost:19188/metrics | grep documentdb_postgres_up; kill %1
```
**Expected:** `documentdb_postgres_up{documentdb_cluster="${DB}",…} 1`.

### 6.10 Lifecycle (LIFE)

#### LIFE-01: Deleting a cluster retains its data volume
```bash
PV=$(kubectl -n "$NS" get pvc "${DB}-restore-1" -o jsonpath='{.spec.volumeName}')
kubectl -n "$NS" delete documentdb "${DB}-restore" --wait=true --timeout=10m
sleep 30
kubectl get pv "$PV" -o custom-columns='NAME:.metadata.name,STATUS:.status.phase,RECLAIM:.spec.persistentVolumeReclaimPolicy'
```
**Expected:** the PV still exists, with `RECLAIM=Retain` and `STATUS=Released`. Recovering a new cluster from this volume is **out of scope** (see section 1); don't try it.

---

## 7. Results and reporting

### Results table
Copy this into the shared tracker, one row per test per cluster.

| Test ID | Cluster | Tester | Result (PASS/FAIL/BLOCKED) | Time / measurement | Bug link | Notes |
|---|---|---|---|---|---|---|
| DEP-01 | | | | time to healthy: | | |
| HA-02 | | | | time to 3 ready: | | |
| HA-04 | | | | failover time: / failed writes: | | |
| … | | | | | | |

### Metrics we collect
| Metric | Where it comes from | Target |
|---|---|---|
| Pass rate per area | Results table | 100% of in-scope tests pass, or have a filed bug |
| Data integrity | DATA-01…10, HA-05 `missing=0`, RES-01, BAK-03 `IDENTICAL` | No lost or corrupted acknowledged writes |
| Time to healthy (new cluster) | DEP-01 | Recorded; typically 1–3 min after the image is cached |
| Scale-out time (1 → 3) | HA-02 | Recorded |
| Failover time and write impact | HA-04 writer log | New primary within ~1 min; writes resume without manual action |
| Database disruption from operator restarts | RES-02 | None (same pod UID, no restarts) |
| Bugs by severity | Bug tracker | No open Sev 1 / Sev 2 at exit |

### Exit criteria
- Every in-scope test was run on at least **two** clusters.
- There are no open Sev 1 or Sev 2 bugs.
- There's no data loss in any scenario.

### Bug report template
```
Title: [Azure Local][<Test ID>] <short summary>
Cluster / location: <CLUSTER> / <LOCATION>   Kubernetes: <version>   Node runtime: <containerd version>
Operator chart: 1.1.0     DocumentDB: 1.0.0     Image mode: combined | imageVolume
Steps: <exact commands from this guide, with any deviations>
Expected: <from this guide>
Actual: <output>
Diagnostics: <attached bundle from section 8>
Severity: Sev1 (data loss/security) | Sev2 (feature broken, no workaround) | Sev3 (workaround exists) | Sev4 (cosmetic/docs)
```
**Security problems:** don't file them in public GitHub issues. Report them privately to the bug bash organizer, who follows the project's `SECURITY.md` process.

---

## 8. Collecting diagnostics
```bash
OUT="diag-${CLUSTER}-${NS}-$(date +%Y%m%d-%H%M%S)"; mkdir -p "$OUT"
kubectl -n "$NS" get documentdb,clusters.postgresql.cnpg.io,pods,svc,pvc -o wide > "$OUT/resources.txt" 2>&1
kubectl -n "$NS" get documentdb -o yaml > "$OUT/documentdb.yaml"
kubectl -n "$NS" get clusters.postgresql.cnpg.io -o yaml > "$OUT/cnpg-cluster.yaml"
kubectl -n "$NS" get events --sort-by=.lastTimestamp > "$OUT/events.txt"
kubectl -n "$NS" describe pods > "$OUT/pods-describe.txt"
for p in $(kubectl -n "$NS" get pods -l cnpg.io/cluster -o name); do
  for c in postgres documentdb-gateway otel-collector; do kubectl -n "$NS" logs "$p" -c "$c" --tail=500 > "$OUT/$(basename "$p")-$c.log" 2>/dev/null; done
done
kubectl -n documentdb-operator logs deploy/documentdb-operator --since=2h > "$OUT/operator.log"
kubectl -n cnpg-system logs deploy/documentdb-operator-cloudnative-pg --since=2h > "$OUT/cnpg-operator.log"
kubectl -n cnpg-system logs deploy/sidecar-injector --since=2h > "$OUT/sidecar-injector.log"
kubectl get nodes -o wide > "$OUT/nodes.txt"
tar czf "$OUT.tgz" "$OUT" && echo "attach $OUT.tgz to the bug"
```
Check the bundle for credentials before sharing it. The secret's values aren't collected, but check the logs you add by hand.

---

## 9. Cleanup (end of the bug bash)
Everyone:
```bash
kubectl delete namespace "$NS" --wait=true --timeout=10m
```

Cluster owner, after everyone has finished. The DocumentDB clusters use `Retain`, so their data volumes are kept until deleted:
```bash
kubectl get pv -o custom-columns='NAME:.metadata.name,STATUS:.status.phase,CLAIM_NS:.spec.claimRef.namespace' | grep -E 'bb-' \
  | awk '$2=="Released"{print $1}' | xargs -r kubectl delete pv
# Optional, only if the clusters will be reused for something else:
# helm uninstall documentdb-operator -n documentdb-operator
```
Stop the `az connectedk8s proxy` terminal when you're done.

---

## Appendix A: Full data-validation script
This runs DATA-01 … DATA-10 in one go and prints one `PASS`/`FAIL` line per area.
```bash
cat > /tmp/bugbash-data.js <<'EOF'
const results=[]; function check(n,f){try{const r=f();results.push(`PASS ${n}${r===undefined?"":" -> "+r}`)}catch(e){results.push(`FAIL ${n} -> ${e.codeName||""} ${e.message}`)}}
const d=db.getSiblingDB("bugbash_auto"); d.dropDatabase();
check("DATA-01 crud",()=>{const c=d.crud;c.insertOne({_id:1,qty:5});c.insertMany([{_id:2,qty:3},{_id:3,qty:8}]);c.updateOne({_id:2},{$inc:{qty:10}});c.replaceOne({_id:3},{qty:1});c.deleteOne({_id:1});const x=c.find().sort({_id:1}).toArray();if(x.length!==2||x[0].qty!==13||x[1].qty!==1)throw new Error(JSON.stringify(x));});
check("DATA-02 bson",()=>{const c=d.types,o=new ObjectId(),w=new Date("2026-01-02T03:04:05Z");c.insertOne({_id:o,i64:NumberLong("9007199254740993"),s:"héllo ✓",n:null,w,b:BinData(0,"AQIDBA=="),x:{a:{b:[1,2,3]}}});const r=c.findOne({_id:o});if(r.i64.toString()!=="9007199254740993"||r.w.getTime()!==w.getTime()||r.s!=="héllo ✓"||r.n!==null||r.x.a.b[2]!==3)throw new Error("mismatch");});
check("DATA-03 regex",()=>{const c=d.re;c.insertMany([{s:"Apple pie"},{s:"apple tart"},{s:"Banana"},{s:"pineapple"}]);const r=[c.countDocuments({s:/^apple/i}),c.countDocuments({s:{$regex:"apple$"}}),c.countDocuments({s:{$not:/apple/i}})].join(",");if(r!=="2,1,1")throw new Error(r);});
check("DATA-04 decimal128",()=>{const c=d.dec;c.insertMany([{a:NumberDecimal("0.1")},{a:NumberDecimal("0.2")}]);const t=c.aggregate([{$group:{_id:null,t:{$sum:"$a"}}}]).toArray()[0].t.toString();if(t!=="0.3")throw new Error(t);});
check("DATA-05 geo",()=>{const c=d.geo;c.insertMany([{n:"Seattle",l:{type:"Point",coordinates:[-122.3321,47.6062]}},{n:"Redmond",l:{type:"Point",coordinates:[-122.1215,47.674]}},{n:"Portland",l:{type:"Point",coordinates:[-122.6765,45.5231]}}]);c.createIndex({l:"2dsphere"});const r=c.find({l:{$near:{$geometry:{type:"Point",coordinates:[-122.3321,47.6062]},$maxDistance:25000}}}).toArray().map(x=>x.n).join(",");if(r!=="Seattle,Redmond")throw new Error(r);});
check("DATA-06 vector",()=>{d.vec.insertMany([{_id:"a",v:[1,0,0]},{_id:"b",v:[0.9,0.1,0]},{_id:"c",v:[0,1,0]},{_id:"d",v:[0,0,1]}]);d.runCommand({createIndexes:"vec",indexes:[{name:"v_hnsw",key:{v:"cosmosSearch"},cosmosSearchOptions:{kind:"vector-hnsw",m:16,efConstruction:64,similarity:"COS",dimensions:3}}]});const r=d.vec.aggregate([{$search:{cosmosSearch:{vector:[1,0.05,0],path:"v",k:2}}}]).toArray().map(x=>x._id).sort().join(",");if(r!=="a,b")throw new Error(r);});
check("DATA-07 aggregation+index",()=>{const c=d.ord;c.insertMany([...Array(1000).keys()].map(i=>({c:"c"+(i%10),q:i%7})));c.createIndex({c:1});const t=c.aggregate([{$group:{_id:"$c",t:{$sum:"$q"}}},{$sort:{t:-1,_id:1}},{$limit:1}]).toArray()[0];if(t._id!=="c3"||t.t!==303)throw new Error(JSON.stringify(t));});
check("DATA-08 unique",()=>{const c=d.u;c.createIndex({e:1},{unique:true});c.insertOne({e:"a"});try{c.insertOne({e:"a"})}catch(e){if(e.code===11000)return;throw e}throw new Error("duplicate accepted");});
check("DATA-09 text",()=>{const c=d.txt;c.insertMany([{t:"fast database on kubernetes"},{t:"slow cooking"}]);c.createIndex({t:"text"});const n=c.countDocuments({$text:{$search:"kubernetes"}});if(n!==1)throw new Error(n);});
check("DATA-10 transaction",()=>{d.tx.insertOne({_id:1,b:100});const s=db.getMongo().startSession(),c=s.getDatabase("bugbash_auto").tx;s.startTransaction();c.updateOne({_id:1},{$inc:{b:-30}});s.abortTransaction();if(d.tx.findOne({_id:1}).b!==100)throw new Error("abort");s.startTransaction();c.updateOne({_id:1},{$inc:{b:-30}});s.commitTransaction();if(d.tx.findOne({_id:1}).b!==70)throw new Error("commit");});
print(results.join("\n"));
EOF
kubectl -n "$NS" cp /tmp/bugbash-data.js mongosh:/tmp/bugbash-data.js
kubectl -n "$NS" exec mongosh -- mongosh --quiet "$URI" /tmp/bugbash-data.js
```
**Expected:** ten `PASS` lines.

## Appendix B: Quick reference
| Need | Command |
|---|---|
| Cluster status | `kubectl -n "$NS" get documentdb "$DB"` |
| Why a mode was chosen | `kubectl -n "$NS" get events --field-selector reason=ImageModeSelected` |
| Current primary | `kubectl -n "$NS" get clusters.postgresql.cnpg.io "$DB" -o jsonpath='{.status.currentPrimary}'` |
| SQL shell (diagnostics only) | `kubectl -n "$NS" exec -it "${DB}-1" -c postgres -- psql -U postgres` |
| Operator logs | `kubectl -n documentdb-operator logs deploy/documentdb-operator -f` |
| Re-open the Arc tunnel | `az connectedk8s proxy -n "$CLUSTER" -g "$RESOURCE_GROUP" --file "$HOME/.kube/${CLUSTER}.yaml" --port 47011` |
