# Application Insights Telemetry Configuration

This document describes how to configure Application Insights telemetry collection for the DocumentDB Kubernetes Operator.

## Overview

The DocumentDB Operator can send telemetry data to Azure Application Insights to help monitor operator health, track cluster lifecycle events, and diagnose issues. All telemetry is designed with privacy in mind - no personally identifiable information (PII) is collected.

## Configuration

### Environment Variables

Configure telemetry by setting these environment variables in the operator deployment:

| Variable | Description | Required |
|----------|-------------|----------|
| `APPINSIGHTS_INSTRUMENTATIONKEY` | Application Insights instrumentation key | Yes (or connection string) |
| `APPLICATIONINSIGHTS_CONNECTION_STRING` | Application Insights connection string (alternative to instrumentation key) | Yes (or instrumentation key) |
| `DOCUMENTDB_TELEMETRY_ENABLED` | Set to `false` to disable telemetry collection | No (default: `true`) |

### Helm Chart Configuration

When installing via Helm, you can configure telemetry in your values.yaml:

```yaml
# values.yaml
telemetry:
  enabled: true
  appInsightsInstrumentationKey: "YOUR-INSTRUMENTATION-KEY-HERE"
  # Or use connection string:
  # appInsightsConnectionString: "InstrumentationKey=xxx;IngestionEndpoint=https://..."
```

### Kubernetes Secret

For production deployments, store the instrumentation key in a Kubernetes secret:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: documentdb-operator-telemetry
  namespace: documentdb-system
type: Opaque
stringData:
  APPINSIGHTS_INSTRUMENTATIONKEY: "YOUR-INSTRUMENTATION-KEY-HERE"
```

Then reference it in the operator deployment:

```yaml
envFrom:
  - secretRef:
      name: documentdb-operator-telemetry
```

## Privacy & Data Collection

### What We Collect

The operator collects anonymous, aggregated telemetry data including:

- **Operator lifecycle**: Startup events, health status, version information
- **Cluster operations**: Create, update, delete events (with timing metrics)
- **Backup operations**: Backup creation, completion, and expiration events
- **Error tracking**: Categorized errors (no raw error messages with sensitive data)
- **Performance metrics**: Reconciliation duration, API call latency

### What We DON'T Collect

To protect your privacy, we explicitly do NOT collect:

- Cluster names, namespace names, or any user-provided resource names
- Connection strings, passwords, or credentials
- IP addresses or hostnames
- Storage class names (may contain organizational information)
- Raw error messages (only categorized error types)
- Container image names

### Privacy Protection Mechanisms

1. **GUIDs Instead of Names**: All resources are identified by auto-generated GUIDs stored in annotations (`telemetry.documentdb.io/cluster-id`)
2. **Hashed Namespaces**: Namespace names are SHA-256 hashed before transmission
3. **Categorized Data**: Values like PVC sizes are categorized (small/medium/large) instead of exact values
4. **Error Sanitization**: Error messages are stripped of potential PII and truncated

## Disabling Telemetry

To completely disable telemetry collection:

1. **Via environment variable**:
   ```yaml
   env:
     - name: DOCUMENTDB_TELEMETRY_ENABLED
       value: "false"
   ```

2. **Via Helm**:
   ```yaml
   telemetry:
     enabled: false
   ```

3. **Don't provide instrumentation key**: If no `APPINSIGHTS_INSTRUMENTATIONKEY` or `APPLICATIONINSIGHTS_CONNECTION_STRING` is set, telemetry is automatically disabled.

## Telemetry Events Reference

See [appinsights-metrics.md](appinsights-metrics.md) for the complete specification of all telemetry events and metrics collected.

## Troubleshooting

### Telemetry Not Being Sent

1. Verify the instrumentation key is correctly configured:
   ```bash
   kubectl get deployment documentdb-operator -n documentdb-system -o yaml | grep -A5 APPINSIGHTS
   ```

2. Check operator logs for telemetry initialization:
   ```bash
   kubectl logs -n documentdb-system -l app=documentdb-operator | grep -i telemetry
   ```

3. Verify network connectivity to Application Insights endpoint (`dc.services.visualstudio.com`)

### High Cardinality Warnings

If you see warnings about high cardinality dimensions, this indicates too many unique values for a dimension. The telemetry system automatically samples high-frequency events to mitigate this.

## Support

For issues related to telemetry collection, please open an issue on the [GitHub repository](https://github.com/documentdb/documentdb-kubernetes-operator/issues).
