# Telemetry and Cost-Optimized EKS (contrib)

> **Status:** community-contributed variant. Not actively maintained by the core DocumentDB team.
> If the base [`documentdb-playground/aws-setup/`](../../aws-setup/) scripts cover your needs, use those instead.

A self-contained variant of the AWS EKS playground that layers CloudWatch-based observability and additional cost-optimization features on top of the base scripts.

## What this variant adds over `aws-setup/`

| Capability | Base `aws-setup/` | This contrib variant |
| --- | --- | --- |
| `--node-type`, `--eks-version`, `--spot`, `--tags` | ✅ | ✅ |
| 2-AZ deployment (cost-reduced) | ❌ (eksctl default: 3) | ✅ |
| CloudFormation stack event diagnostics | ❌ | ✅ |
| `mongosh` prerequisite warning | ❌ | ✅ |
| S3 Gateway VPC endpoint (free) | ❌ | ✅ |
| EKS control-plane logging → CloudWatch | ❌ | ✅ (`--control-plane-log-types`) |
| CloudWatch log-group retention | ❌ | ✅ (`--log-retention`) |
| Amazon CloudWatch Observability add-on (Container Insights) | ❌ | ✅ |
| CloudWatch log-group teardown | ❌ | ✅ |
| CloudWatch-aware post-deploy diagnostics | ❌ | ✅ |

The default cluster name is `documentdb-contrib-cluster` so this variant can run alongside the base setup.

## Prerequisites

Same as [`aws-setup/`](../../aws-setup/) — AWS CLI, `eksctl`, `kubectl`, `helm`, `jq` — plus:

- `mongosh` (warned, not required) for local endpoint validation.
- IAM permissions to manage EKS add-ons, CloudWatch log groups, VPC endpoints, and pod identity associations.

## Quick start

```bash
./scripts/create-cluster.sh --deploy-instance
# ...wait for cluster + add-on to become ACTIVE...
./scripts/delete-cluster.sh -y
```

See `./scripts/create-cluster.sh --help` for the full list of options.

## Script options

### Simple (same as `aws-setup/`)

- `--node-type TYPE` — EC2 instance type (default: `m7g.large` Graviton/ARM)
- `--eks-version VER` — Kubernetes/EKS version (default: `1.35`)
- `--spot` — Spot-backed managed nodes (dev/test only; see warning below)
- `--tags TAGS` — comma-separated `key=value` pairs for AWS cost allocation

### Contrib-only (logging / observability)

- `--log-retention DAYS` — CloudWatch retention in days (default: `3`). Valid: `1,3,5,7,14,30,60,90,120,150,180,365,400,545,731,1827,3653`.
- `--control-plane-log-types LIST` — comma-separated EKS control-plane log types (default: `api,authenticator`). Valid: `api,audit,authenticator,controllerManager,scheduler`. Keep this list small to control cost.

### Spot Instance Warning

When using `--spot`, AWS can terminate instances at any time with 2 minutes notice. This **will interrupt your database** and require recovery. Only use Spot for dev/test.

## Logging model

All pod stdout/stderr, EKS control-plane events, and host/cluster telemetry flow into **Amazon CloudWatch Logs** via the managed Amazon CloudWatch Observability EKS add-on. No hand-rolled Fluent Bit / DaemonSet manifests are maintained in this variant — the add-on owns the collector.

Log groups created for the cluster (retention set by `--log-retention`, default 3 days):

| Log group | Contents |
| --- | --- |
| `/aws/eks/<CLUSTER>/cluster` | EKS control-plane logs (types selected by `--control-plane-log-types`) |
| `/aws/containerinsights/<CLUSTER>/application` | Pod stdout/stderr (operator, DocumentDB instance, everything else) |
| `/aws/containerinsights/<CLUSTER>/dataplane` | System pods, kubelet, kube-proxy |
| `/aws/containerinsights/<CLUSTER>/host` | Node OS logs |
| `/aws/containerinsights/<CLUSTER>/performance` | Container Insights performance metrics |

Example queries:

```bash
# Live tail all pod stdout/stderr
aws logs tail /aws/containerinsights/$CLUSTER_NAME/application --region $REGION --since 1h --follow

# Just the operator namespace
aws logs tail /aws/containerinsights/$CLUSTER_NAME/application --region $REGION \
  --filter-pattern '{ $.kubernetes.namespace_name = "documentdb-operator" }' --since 1h

# Just one DocumentDB instance pod
aws logs tail /aws/containerinsights/$CLUSTER_NAME/application --region $REGION \
  --filter-pattern '{ $.kubernetes.pod_name = "sample-documentdb-1" }' --since 1h

# EKS API server audit
aws logs tail /aws/eks/$CLUSTER_NAME/cluster --region $REGION --since 1h
```

## Cost optimization

| Area | Optimization |
| --- | --- |
| Compute | `m7g.large` Graviton default (~20% cheaper than equivalent x86) |
| Compute (dev/test) | `--spot` for ~70% savings |
| Networking | 2-AZ deployment (minimum EKS supports) reduces cross-AZ data transfer |
| Networking | S3 Gateway VPC endpoint is free and eliminates NAT Gateway data-transfer cost for S3 |
| Storage | gp3 storage class (already in `aws-setup/`, inherited here) |
| Logging | `--log-retention 3` (3-day default) and narrow `--control-plane-log-types api,authenticator` keep CloudWatch bills bounded |
| Attribution | `--tags`/`CLUSTER_TAGS` for Cost Explorer breakdown |

**Rough estimate:** dev/test cluster with `--spot`, `--deploy-instance`, and default logging lands in the low tens of dollars per month. Always run `delete-cluster.sh` when done.

## Troubleshooting

See the troubleshooting block printed by `create-cluster.sh --deploy-instance` (it includes `aws logs tail` examples and port-forward + mongosh validation steps). The main entry points are:

1. `kubectl get pods -n documentdb-instance-ns` — are the pods Running?
2. `aws logs tail /aws/containerinsights/<CLUSTER>/application --region <REGION> --since 1h --follow` — what do the pods say?
3. `aws eks describe-addon --cluster-name <CLUSTER> --addon-name amazon-cloudwatch-observability --region <REGION>` — is the collector healthy?
4. `kubectl port-forward -n documentdb-instance-ns svc/documentdb-service-sample-documentdb 10260:10260` + `mongosh` — does the endpoint work independently of the app?

## Teardown

`./scripts/delete-cluster.sh -y` removes:

- DocumentDB instances, operator, and related Helm releases
- The CloudWatch Observability add-on (waits for `addon-deleted`)
- VPC endpoints (so the VPC can be destroyed)
- All CloudWatch log groups for the cluster
- The EKS cluster itself (all CloudFormation stacks)

## Related

- Base scripts: [`documentdb-playground/aws-setup/`](../../aws-setup/)
- Simple options ship in documentdb#349: `NODE_TYPE`, `EKS_VERSION`, `CLUSTER_TAGS`, `USE_SPOT`.
