# DocumentDB Kubernetes Operator - AWS EKS Deployment

Simple automation scripts for deploying DocumentDB operator on AWS EKS.

## Prerequisites

- AWS CLI configured: `aws configure`
- Required tools: `aws`, `eksctl`, `kubectl`, `helm`, `jq`
- **For operator installation**: GitHub account with access to microsoft/documentdb-operator

### GitHub Authentication (Required for Operator)

To install the DocumentDB operator, you need GitHub Container Registry access:

1. **Create GitHub Personal Access Token**:
   - Go to https://github.com/settings/tokens
   - Click "Generate new token (classic)"
   - Select scope: `read:packages`
   - Copy the generated token

2. **Set Environment Variables**:
   ```bash
   export GITHUB_USERNAME="your-github-username"
   export GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"
   ```

## Quick Start

```bash
# Create EKS cluster with DocumentDB (includes public IP LoadBalancer)
export GITHUB_USERNAME="your-github-username"
export GITHUB_TOKEN="ghp_xxxxxxxxxxxxxxxxxxxx"
./scripts/create-cluster.sh --deploy-instance

# Delete cluster when done (avoid charges)
./scripts/delete-cluster.sh

# OR keep cluster and delete DocumentDB components
./scripts/delete-cluster.sh --instance-and-operator
```

## Load Balancer Configuration

The DocumentDB service is automatically configured with these annotations for public IP access:

```yaml
serviceAnnotations:
  service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
  service.beta.kubernetes.io/aws-load-balancer-scheme: "internet-facing"
  service.beta.kubernetes.io/aws-load-balancer-cross-zone-load-balancing-enabled: "true"
  service.beta.kubernetes.io/aws-load-balancer-nlb-target-type: "ip"
```

**Note**: These annotations are automatically applied by the DocumentDB operator when `environment: eks` is specified in the DocumentDB resource. Manual patching is no longer required.

**Note**: It takes 2-5 minutes for AWS to provision the Network Load Balancer and assign a public IP.

## Script Options

### create-cluster.sh
```bash
# Basic usage (cluster only)
./scripts/create-cluster.sh

# With operator using environment variables
export GITHUB_USERNAME="your-username"
export GITHUB_TOKEN="your-token"
./scripts/create-cluster.sh --install-operator

# With operator using command-line parameters
./scripts/create-cluster.sh --install-operator \
  --github-username "your-username" \
  --github-token "your-token"

# Custom configuration
./scripts/create-cluster.sh --cluster-name my-cluster --region us-east-1

# See all options
./scripts/create-cluster.sh --help
```

**Available options:**
- `--cluster-name NAME` - EKS cluster name (default: documentdb-cluster)
- `--region REGION` - AWS region (default: us-west-2)
- `--skip-operator` - Skip operator installation (default)
- `--install-operator` - Install operator (requires GitHub authentication)
- `--deploy-instance` - Deploy operator + instance (requires GitHub authentication)
- `--node-type TYPE` - EC2 instance type (default: `m7g.large`, Graviton/ARM)
- `--eks-version VER` - Kubernetes/EKS version (default: `1.35`)
- `--spot` - Use Spot-backed managed nodes (dev/test only — see warning below)
- `--tags TAGS` - Cost allocation tags as comma-separated `key=value` pairs (default: `project=documentdb-playground,environment=dev,managed-by=eksctl`)

#### Spot Instance Warning

When using `--spot`, AWS can terminate instances at any time with only 2 minutes notice.
This **will interrupt your database** and require recovery. Only use Spot for dev/test
workloads where brief downtime is acceptable. Spot is disabled by default.

#### Custom Tags

Tags are passed to AWS for cost allocation tracking in Cost Explorer:

```bash
# Default tags
./scripts/create-cluster.sh
# Tags: project=documentdb-playground,environment=dev,managed-by=eksctl

# Custom tags via flag
./scripts/create-cluster.sh --tags "project=myproj,team=platform,costcenter=1234"

# Or via environment variable
export CLUSTER_TAGS="project=myproj,team=platform"
./scripts/create-cluster.sh
```

### delete-cluster.sh
```bash
# Delete everything (default)
./scripts/delete-cluster.sh

# Delete only DocumentDB instances (keep operator and cluster)
./scripts/delete-cluster.sh --instance-only

# Delete instances and operator (keep cluster)
./scripts/delete-cluster.sh --instance-and-operator

# Custom configuration  
./scripts/delete-cluster.sh --cluster-name my-cluster --region us-east-1

# See all options
./scripts/delete-cluster.sh --help
```

**Available options:**
- `--cluster-name NAME` - EKS cluster name (default: documentdb-cluster)
- `--region REGION` - AWS region (default: us-west-2)
- `--instance-only` - Delete only DocumentDB instances
- `--instance-and-operator` - Delete instances and operator (keep cluster)

**Common scenarios:**
- **Default**: Delete everything (instances + operator + cluster)
- **Cost optimization**: Use `--instance-and-operator` to preserve expensive EKS setup
- **Testing instances**: Use `--instance-only` to test deployments without recreating operator
- **Operator upgrades**: Use `--instance-and-operator` to reinstall operator without losing cluster

## What Gets Created

**create-cluster.sh builds:**
- EKS cluster with managed nodes (default: `m7g.large` Graviton/ARM, 3 nodes)
- EBS CSI driver for storage
- AWS Load Balancer Controller
- cert-manager for TLS
- Optimized storage classes

**Estimated cost:** ~$140-230/month (always run delete-cluster.sh when done!) — use `--spot` for ~70% savings on dev/test.

## Support

- [GitHub Issues](https://github.com/documentdb/documentdb-kubernetes-operator/issues)
- [Documentation](https://documentdb.github.io/documentdb-kubernetes-operator)