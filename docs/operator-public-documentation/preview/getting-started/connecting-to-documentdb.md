---
title: Connecting to DocumentDB
description: Connect to DocumentDB using mongosh, connection strings, and MongoDB-compatible drivers in Python, Node.js, Go, and Java.
tags:
  - getting-started
  - connection
  - drivers
  - python
  - nodejs
  - go
  - java
search:
  boost: 2
---

# Connecting to DocumentDB

DocumentDB exposes a MongoDB-compatible wire protocol through the DocumentDB Gateway on port **10260**. You can connect using `mongosh`, any MongoDB-compatible driver, or tools that support the MongoDB protocol.

## Connection string format

The standard connection string format for DocumentDB:

```text
mongodb://<username>:<password>@<host>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0
```



| Parameter | Description |
|-----------|-------------|
| `<username>` | Username from your `documentdb-credentials` Secret |
| `<password>` | Password from your `documentdb-credentials` Secret |
| `<host>` | `127.0.0.1` (port-forward), LoadBalancer IP, or service DNS name |
| `directConnection=true` | Connect directly to the gateway (required) |
| `authMechanism=SCRAM-SHA-256` | Authentication mechanism (required) |
| `tls=true` | TLS is always enabled on the gateway |
| `tlsAllowInvalidCertificates=true` | Skip certificate validation (for self-signed certificates) |
| `replicaSet=rs0` | Replica set name |

!!! note
  You only need `tlsAllowInvalidCertificates=true` (or `--tlsAllowInvalidCertificates`) when using self-signed certificates. For trusted CA-issued certificates, omit it.

!!! tip "Get the connection string from the DocumentDB cluster status"
    The operator generates a connection string in the DocumentDB resource status. This string contains embedded `kubectl` commands to extract credentials from the Secret:

    ```bash
    kubectl get documentdb my-documentdb -n documentdb-ns -o jsonpath='{.status.connectionString}'
    ```

    To get a usable connection string with resolved credentials:

    ```bash
    NAMESPACE="documentdb-ns"
    SECRET="documentdb-credentials"
    USER=$(kubectl get secret $SECRET -n $NAMESPACE -o jsonpath='{.data.username}' | base64 -d)
    PASS=$(kubectl get secret $SECRET -n $NAMESPACE -o jsonpath='{.data.password}' | base64 -d)
    HOST="127.0.0.1"  # Change to LoadBalancer IP for external access
    echo "mongodb://${USER}:${PASS}@${HOST}:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
    ```

## Access methods

### Port forwarding (local development)

For `ClusterIP` services, use kubectl port-forward:

```bash
kubectl port-forward pod/my-documentdb-1 10260:10260 -n documentdb-ns
```

Then connect to `127.0.0.1:10260`.

### LoadBalancer (cloud deployments)

For `LoadBalancer` services, the gateway is reachable through the external IP:

```bash
kubectl get svc documentdb-service-my-documentdb -n documentdb-ns \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

The service name follows the pattern `documentdb-service-<documentdb-name>` (truncated to 63 characters).

!!! note
    Set `spec.environment` to `aks`, `eks`, or `gke` to apply cloud-specific load balancer annotations automatically. See [Networking](../configuration/networking.md) for details.

### Cross-cloud connectivity (multi-region deployments)

For multi-region DocumentDB deployments with cross-cluster replication, the operator supports two networking strategies configured via `spec.clusterReplication.crossCloudNetworkingStrategy`:

=== "Istio (recommended for multi-cloud)"

    Istio service mesh handles cross-cluster service discovery and mTLS traffic through east-west gateways. Each Kubernetes cluster runs its own Istio control plane, and remote secrets enable mutual discovery. Applications connect to DocumentDB using the LoadBalancer IP of the Istio east-west gateway or the DocumentDB service in the local Kubernetes cluster.

    See the [multi-cloud deployment playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/multi-cloud-deployment) for a complete AKS + GKE + EKS setup with Istio.

=== "Azure Fleet"

    Azure Fleet networking uses `ServiceExport` and `MultiClusterService` resources to expose DocumentDB services across fleet member Kubernetes clusters. The operator automatically generates cross-cluster service endpoints using the naming pattern:

    ```text
    <namespace>-<service-name>.fleet-system.svc
    ```

    See the [AKS Fleet deployment playground](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/aks-fleet-deployment) for setup details.

??? tip "Azure DNS for production connection strings"
    The multi-cloud playground can optionally create an **Azure DNS zone** with:

    - **A/CNAME records** for each Kubernetes cluster's DocumentDB LoadBalancer endpoint (for example, `azure-documentdb.<zone>`, `gcp-documentdb.<zone>`)
    - An **SRV record** (`_mongodb._tcp.<zone>`) pointing to the primary DocumentDB cluster on port 10260

    This enables `mongodb+srv://` connection strings that automatically resolve to the correct endpoint:

    ```bash
    mongosh "mongodb+srv://docdb:<password>@<zone-name>.<parent-zone>/?tls=true&tlsAllowInvalidCertificates=true&authMechanism=SCRAM-SHA-256"
    ```

    Set `ENABLE_AZURE_DNS=true` when running `deploy-documentdb.sh` in the multi-cloud playground. See the [multi-cloud deployment README](https://github.com/documentdb/documentdb-kubernetes-operator/tree/main/documentdb-playground/multi-cloud-deployment) for configuration options.

### Kubernetes service DNS (in-cluster)

Applications running inside the same Kubernetes cluster can connect using the service DNS name:

```text
documentdb-service-my-documentdb.documentdb-ns.svc.cluster.local:10260
```


## Connect with mongosh

```bash
mongosh "mongodb://dev_user:DevPassword123@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0"
```

Or use individual flags:

```bash
mongosh 127.0.0.1:10260 \
  -u dev_user \
  -p DevPassword123 \
  --authenticationMechanism SCRAM-SHA-256 \
  --tls --tlsAllowInvalidCertificates
```

## Connect with DocumentDB for VS Code

The [DocumentDB for VS Code](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-documentdb) extension provides a graphical interface for connecting to DocumentDB, browsing databases and collections, and running queries directly from Visual Studio Code.

1. Install the **DocumentDB for VS Code** extension from the [VS Code Marketplace](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.vscode-documentdb).
2. Open the DocumentDB view in the Activity Bar.
3. Select **Add Connection** and enter your connection string:

    ```text
    mongodb://dev_user:DevPassword123@127.0.0.1:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0
    ```

4. Browse databases, collections, and documents. You can also use the built-in playground to run queries interactively.

!!! tip
    If you are already using VS Code with the [Dev Containers](https://marketplace.visualstudio.com/items?itemName=ms-vscode-remote.remote-containers) extension for operator development, the DocumentDB extension works inside the devcontainer as well — just forward port 10260.

## Driver examples

All examples below assume:

- DocumentDB is accessible at `127.0.0.1:10260` (via port-forward or LoadBalancer)
- Credentials: username `dev_user`, password `DevPassword123`
- Self-signed TLS (certificate validation skipped)

For production deployments with trusted certificates, see [TLS configuration](../configuration/tls.md).

### Python (PyMongo)

Install the driver:

```bash
pip install pymongo
```

```python title="connect_documentdb.py"
from pymongo import MongoClient

client = MongoClient(
    host="127.0.0.1",
    port=10260,
    username="dev_user",
    password="DevPassword123",
    authSource="admin",
    authMechanism="SCRAM-SHA-256",
    tls=True,
    tlsAllowInvalidCertificates=True,
    directConnection=True,
    # Connection pool settings
    maxPoolSize=50,       # Maximum connections in the pool
    minPoolSize=5,        # Minimum idle connections
    maxIdleTimeMS=30000,  # Close idle connections after 30 seconds
)

# Insert a document
db = client["testdb"]
result = db.users.insert_one({"name": "Alice", "role": "admin"})
print(f"Inserted: {result.inserted_id}")

# Query documents
for doc in db.users.find():
    print(doc)

client.close()
```

### Node.js (MongoDB Driver)

Install the driver:

```bash
npm install mongodb
```

```javascript title="connect_documentdb.js"
const { MongoClient } = require("mongodb");

const uri =
  "mongodb://dev_user:DevPassword123@127.0.0.1:10260/" +
  "?directConnection=true&authMechanism=SCRAM-SHA-256" +
  "&tls=true&tlsAllowInvalidCertificates=true&replicaSet=rs0";

const client = new MongoClient(uri, {
  // Connection pool settings
  maxPoolSize: 50,
  minPoolSize: 5,
  maxIdleTimeMS: 30000,
});

async function main() {
  try {
    await client.connect();

    const db = client.db("testdb");
    const result = await db.collection("users").insertOne({
      name: "Alice",
      role: "admin",
    });
    console.log(`Inserted: ${result.insertedId}`);

    const docs = await db.collection("users").find().toArray();
    console.log(docs);
  } finally {
    await client.close();
  }
}

main().catch(console.error);
```

### Go (MongoDB Go Driver)

Install the driver:

```bash
go get go.mongodb.org/mongo-driver/mongo
```

```go title="connect_documentdb.go"
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	uri := "mongodb://dev_user:DevPassword123@127.0.0.1:10260/" +
		"?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true"

	// For self-signed certificates, skip verification.
	// For trusted CAs, load the CA cert into a tls.Config instead.
	tlsConfig := &tls.Config{InsecureSkipVerify: true}

	clientOpts := options.Client().
		ApplyURI(uri).
		SetTLSConfig(tlsConfig).
		SetMaxPoolSize(50).    // Maximum connections in the pool
		SetMinPoolSize(5).     // Minimum idle connections
		SetMaxConnIdleTime(30 * time.Second)

	client, err := mongo.Connect(ctx, clientOpts)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(ctx)

	coll := client.Database("testdb").Collection("users")
	result, err := coll.InsertOne(ctx, bson.M{"name": "Alice", "role": "admin"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Inserted: %v\n", result.InsertedID)

	cursor, err := coll.Find(ctx, bson.M{})
	if err != nil {
		log.Fatal(err)
	}
	defer cursor.Close(ctx)

	var docs []bson.M
	if err := cursor.All(ctx, &docs); err != nil {
		log.Fatal(err)
	}
	for _, doc := range docs {
		fmt.Println(doc)
	}
}
```

!!! note "Go driver and `replicaSet`"
    Do not include `replicaSet=rs0` in the URI when using `directConnection=true` with the Go driver. The combination creates a conflicting topology configuration that prevents the driver from connecting. The `directConnection=true` parameter alone is sufficient.

### Java (MongoDB Java Driver)

Add the dependency to your `pom.xml`:

```xml title="pom.xml (dependency)"
<dependency>
    <groupId>org.mongodb</groupId>
    <artifactId>mongodb-driver-sync</artifactId>
    <version>5.4.0</version>
</dependency>
```

```java title="ConnectDocumentDB.java"
import com.mongodb.ConnectionString;
import com.mongodb.MongoClientSettings;
import com.mongodb.client.MongoClient;
import com.mongodb.client.MongoClients;
import com.mongodb.client.MongoCollection;
import com.mongodb.client.MongoDatabase;
import org.bson.Document;

import javax.net.ssl.SSLContext;
import javax.net.ssl.TrustManager;
import javax.net.ssl.X509TrustManager;
import java.security.cert.X509Certificate;
import java.util.concurrent.TimeUnit;

public class ConnectDocumentDB {
    public static void main(String[] args) throws Exception {
        String uri = "mongodb://dev_user:DevPassword123@127.0.0.1:10260/"
            + "?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true";

        // For self-signed certificates, create a trust-all SSLContext.
        // For trusted CAs, configure a proper TrustStore instead.
        TrustManager[] trustAll = new TrustManager[]{
            new X509TrustManager() {
                public X509Certificate[] getAcceptedIssuers() { return new X509Certificate[0]; }
                public void checkClientTrusted(X509Certificate[] c, String t) {}
                public void checkServerTrusted(X509Certificate[] c, String t) {}
            }
        };
        SSLContext sslContext = SSLContext.getInstance("TLS");
        sslContext.init(null, trustAll, new java.security.SecureRandom());

        MongoClientSettings settings = MongoClientSettings.builder()
            .applyConnectionString(new ConnectionString(uri))
            .applyToSslSettings(builder -> builder
                .enabled(true)
                .context(sslContext))
            .applyToConnectionPoolSettings(builder -> builder
                .maxSize(50)           // Maximum connections in the pool
                .minSize(5)            // Minimum idle connections
                .maxConnectionIdleTime(30, TimeUnit.SECONDS))
            .build();

        try (MongoClient client = MongoClients.create(settings)) {
            MongoDatabase db = client.getDatabase("testdb");
            MongoCollection<Document> coll = db.getCollection("users");

            coll.insertOne(new Document("name", "Alice").append("role", "admin"));
            System.out.println("Inserted document");

            for (Document doc : coll.find()) {
                System.out.println(doc.toJson());
            }
        }
    }
}
```

!!! note "Java driver TLS with self-signed certificates"
    The `tlsAllowInvalidCertificates=true` URI parameter does not work with the Java driver. You must provide an explicit `SSLContext` that trusts the server certificate. For production, load your CA certificate into a JKS/PKCS12 TrustStore instead of using the trust-all approach shown above.

## Connection pooling best practices

MongoDB drivers maintain a connection pool to reuse TCP connections and reduce latency. Follow these best practices:

| Setting | Recommended | Description |
|---------|-------------|-------------|
| `maxPoolSize` | 50–100 | Maximum number of connections. Tune based on your workload. |
| `minPoolSize` | 5–10 | Keep idle connections warm to avoid cold-start latency. |
| `maxIdleTimeMS` | 30000 | Close connections idle for more than 30 seconds. |
| `connectTimeoutMS` | 10000 | Timeout for establishing new connections. |
| `serverSelectionTimeoutMS` | 30000 | Timeout for selecting a server from the topology. |

!!! warning "Avoid creating a new client per request"
    Create a **single** `MongoClient` instance and reuse it for the lifetime of your application. The driver manages connection pooling internally. Creating a new client per request exhausts connections and degrades performance.

!!! tip "Connection pool sizing"
    A good starting point is `maxPoolSize = number_of_concurrent_requests × 1.5`. Monitor connection wait times and adjust as needed. If your application runs multiple replicas (pods), divide the pool size by the number of replicas to avoid overwhelming the database.

## TLS configuration

DocumentDB Gateway always serves TLS. The TLS mode is configurable via the `spec.tls` field:

| Mode | Description | Certificate validation |
|------|-------------|----------------------|
| `SelfSigned` (default) | Operator generates a self-signed certificate | Use `tlsAllowInvalidCertificates=true` |
| `CertManager` | cert-manager issues a trusted certificate | Validate against your CA bundle |
| `Provided` | You supply your own certificate Secret | Validate against your CA |
| `Disabled` | TLS disabled on the gateway | Not recommended |

### Connecting with a trusted certificate

When using `CertManager` or `Provided` mode with a trusted CA, remove `tlsAllowInvalidCertificates` and provide the CA certificate:

=== "mongosh"

    ```bash
    mongosh "mongodb://dev_user:DevPassword123@<host>:10260/?directConnection=true&authMechanism=SCRAM-SHA-256&tls=true&replicaSet=rs0" \
      --tlsCAFile /path/to/ca.crt
    ```

=== "Python"

    ```python
    client = MongoClient(
        host="<host>",
        port=10260,
        username="dev_user",
        password="DevPassword123",
        authSource="admin",
        authMechanism="SCRAM-SHA-256",
        tls=True,
        tlsCAFile="/path/to/ca.crt",
        directConnection=True,
    )
    ```

=== "Node.js"

    ```javascript
    const fs = require("fs");
    const client = new MongoClient(uri, {
      tls: true,
      tlsCAFile: "/path/to/ca.crt",
    });
    ```

=== "Go"

    ```go
    // Load CA certificate
    caCert, _ := os.ReadFile("/path/to/ca.crt")
    caCertPool := x509.NewCertPool()
    caCertPool.AppendCertsFromPEM(caCert)

    tlsConfig := &tls.Config{RootCAs: caCertPool}

    clientOpts := options.Client().
        ApplyURI(uri).
        SetTLSConfig(tlsConfig)
    ```

=== "Java"

    ```java
    // Set the CA certificate via JVM trust store
    System.setProperty("javax.net.ssl.trustStore", "/path/to/truststore.jks");
    System.setProperty("javax.net.ssl.trustStorePassword", "changeit");
    ```

For complete TLS configuration details, see [TLS](../configuration/tls.md).

## Troubleshooting

### Connection refused

**Symptom:** `MongoServerSelectionError: connect ECONNREFUSED`

- Verify the pod is running: `kubectl get pods -n documentdb-ns`
- Verify port-forward is active (if using ClusterIP)
- Check that you are using port **10260** (DocumentDB Gateway), not 5432 (PostgreSQL)

### Authentication failed

**Symptom:** `MongoServerError: Authentication failed`

- Verify the credentials match the Secret:
  ```bash
  kubectl get secret documentdb-credentials -n documentdb-ns -o jsonpath='{.data.username}' | base64 -d
  ```
- Ensure `authMechanism=SCRAM-SHA-256` is specified
- Ensure `authSource=admin` (default for most drivers)

### TLS handshake errors

**Symptom:** `SSL routines:ssl3_get_record:wrong version number` or `certificate verify failed`

- Add `tlsAllowInvalidCertificates=true` for self-signed certificates
- Ensure `tls=true` is in the connection string (TLS is always enabled)
- If using a trusted CA, verify the CA file path is correct

### Connection timeout

**Symptom:** `MongoServerSelectionError: Server selection timed out`

- For LoadBalancer services, verify the external IP is assigned:
  ```bash
  kubectl get svc -n documentdb-ns
  ```
- Check network policies or firewall rules blocking port 10260
- Ensure `directConnection=true` is set

## Next steps

- [Networking](../configuration/networking.md) — LoadBalancer configuration and Network Policies
- [TLS](../configuration/tls.md) — certificate management modes
- [API Reference](../api-reference.md) — full CRD field reference
