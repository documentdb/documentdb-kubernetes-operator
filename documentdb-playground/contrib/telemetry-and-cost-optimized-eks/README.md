# Telemetry and Cost-Optimized EKS (contrib)

> **Status:** community-contributed variant. Not actively maintained by the core DocumentDB team.

This folder contains a self-contained variant of the AWS EKS playground that layers additional cost-optimization and observability features on top of the base scripts in [`documentdb-playground/aws-setup/`](../../aws-setup/).

At this scaffold commit it is functionally equivalent to the base `aws-setup/` scripts plus the four simple options (`--node-type`, `--eks-version`, `--spot`, `--tags`). Subsequent commits add:

- 2-AZ deployment and CloudFormation event diagnostics
- S3 Gateway VPC endpoint (free — reduces NAT Gateway data-transfer costs)
- EKS control-plane logging + CloudWatch log-group retention
- Amazon CloudWatch Observability add-on (Container Insights)
- CloudWatch-aware DocumentDB diagnostics

The default cluster name is `documentdb-contrib-cluster` so you can run this alongside the base setup without collisions.

## Quick start

```bash
./scripts/create-cluster.sh --deploy-instance
./scripts/delete-cluster.sh -y
```

See `./scripts/create-cluster.sh --help` for the full list of options. A richer README covering the full cost model and troubleshooting story lands in a later commit on this branch.
