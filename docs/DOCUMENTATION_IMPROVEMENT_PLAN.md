# Documentation Improvement Plan

**Created:** February 12, 2026  
**Goal:** Deliver comprehensive, production-ready documentation following Kubernetes operator best practices

---

## Proposed Documentation Structure

```
docs/operator-public-documentation/
├── index.md                              # Landing page
├── getting-started/
│   ├── before-you-start.md               # Terminology, prerequisites
│   ├── quickstart-kind.md                # Quick start with Kind (local dev)
│   ├── quickstart-k3s.md                 # Quick start with K3s (lightweight)
│   ├── installation.md                   # Detailed installation
│   ├── deploy-on-aks.md                  # Azure AKS deployment guide
│   ├── deploy-on-eks.md                  # AWS EKS deployment guide
│   ├── deploy-on-gke.md                  # GCP GKE deployment guide
│   └── connecting-to-documentdb.md       # Application connection guide
├── architecture/
│   └── overview.md                       # Architecture overview with diagrams
├── configuration/
│   ├── cluster-configuration.md          # DocumentDB CRD options
│   ├── storage.md                        # Storage configuration
│   ├── networking.md                     # Services, load balancers
│   ├── tls.md                            # TLS modes and setup
│   └── resource-management.md            # CPU, memory, scaling
├── operations/
│   ├── backup-and-restore.md             # Enhanced backup docs
│   ├── scaling.md                        # Scaling procedures
│   ├── upgrades.md                       # Operator and cluster upgrades
│   ├── failover.md                       # Failover procedures
│   ├── restore-deleted-cluster.md        # Restore a deleted cluster
│   └── maintenance.md                    # Node maintenance, rolling updates
├── high-availability/
│   ├── overview.md                       # HA concepts, types (local, multi-region, multi-cloud)
│   └── local-ha.md                       # Local HA configuration
├── multi-region-deployment/
│   ├── overview.md                       # Multi-region concepts and planning
│   ├── setup.md                          # Multi-region setup guide
│   └── failover-procedures.md            # Cross-region failover runbooks
├── multi-cloud-deployment/
│   ├── overview.md                       # Multi-cloud concepts and planning
│   ├── setup.md                          # Multi-cloud setup guide (AKS + GKE + EKS)
│   └── failover-procedures.md            # Cross-cloud failover runbooks
├── security/
│   ├── overview.md                       # Security model
│   ├── rbac.md                           # RBAC configuration
│   ├── network-policies.md               # Network security
│   └── secrets-management.md             # Credentials and secrets
├── monitoring/
│   ├── overview.md                       # Monitoring setup
│   └── metrics.md                        # Prometheus metrics reference
├── tuning/
│   └── tuning-guide.md                   # Performance tuning guide
├── troubleshooting/
│   ├── diagnostic-tools.md               # Logs, events, debugging
│   └── common-issues.md                  # Known issues and solutions
├── reference/
│   ├── api-reference.md                  # CRD documentation
│   ├── kubectl-plugin.md                 # Existing plugin docs
│   └── labels-annotations.md             # Labels/annotations reference
├── faq.md                                # Expanded FAQ
├── release-notes.md                      # Version history
├── support.md                            # Get help and support
├── privacy.md                            # Data collection and privacy
├── contributing.md                       # How to contribute (links to GitHub)
└── security-reporting.md                 # How to report security issues
```

---

## Tasks

### Phase 1: Foundation

#### 1.1 Terminology and Prerequisites Page
**File:** `getting-started/before-you-start.md`

Create a terminology page covering:
- Kubernetes concepts (Pod, Service, PVC, StorageClass, Namespace, CRD, Operator)
- DocumentDB concepts (Instance, Primary, Replica, Gateway, Cluster)
- Cloud concepts (Region, Availability Zone)
- Prerequisites checklist with version requirements

---

#### 1.2 Architecture Overview Page
**File:** `architecture/overview.md`

Create architecture documentation including:
- System architecture diagram (Mermaid or image)
- Component descriptions (Operator, DocumentDB Gateway, database instances)
- Data flow explanation
- Kubernetes resource relationships

---

#### 1.3 Expand FAQ Page
**File:** `faq.md`

Expand FAQ from 1 to 15+ entries covering:
- What is DocumentDB Kubernetes Operator?
- What Kubernetes distributions are supported? (AKS, EKS, GKE, Kind, K3s, etc.)
- Can I run DocumentDB in production on Kubernetes?
- What are the minimum resource requirements?
- What happens during a failover?
- How do backups work?
- What storage classes are recommended?
- How does the operator handle upgrades?
- What is the difference between HA and DR?
- How do I report a bug or request a feature?

*Note: Avoid questions better answered by dedicated guides (e.g., "How do I connect my app?" → see connecting-to-documentdb.md)*

---

#### 1.4 API Reference
**File:** `reference/api-reference.md`

Generate comprehensive API reference for:
- `DocumentDB` CRD (all spec/status fields)
- `Backup` CRD (all spec/status fields)
- `ScheduledBackup` CRD (all spec/status fields)

Include field types, defaults, validation rules, and examples.

---

#### 1.5 Deploy on AKS Guide
**File:** `getting-started/deploy-on-aks.md`

Document Azure AKS deployment (reference `documentdb-playground/aks-setup`):
- Overview and prerequisites
- Quick start using playground scripts
- Azure-specific storage classes explained (StandardSSD_LRS, Premium SSD)
- Azure Load Balancer annotations explained
- Azure CNI networking considerations
- cert-manager setup on AKS
- Verification steps
- Troubleshooting common AKS issues

---

#### 1.6 Deploy on EKS Guide
**File:** `getting-started/deploy-on-eks.md`

Document AWS EKS deployment (reference `documentdb-playground/aws-setup`):
- Overview and prerequisites
- Quick start using playground scripts
- AWS-specific storage classes explained (gp3, io1)
- NLB annotations for external access explained
- IAM and IRSA considerations
- Verification steps
- Troubleshooting common EKS issues

---

#### 1.7 Deploy on GKE Guide
**File:** `getting-started/deploy-on-gke.md`

Document GCP GKE deployment:
- Overview and prerequisites
- GCP-specific storage classes explained (pd-standard, pd-ssd)
- GCP Load Balancer annotations explained
- Workload Identity considerations
- Verification steps
- Troubleshooting common GKE issues

*Note: Create `documentdb-playground/gke-setup` scripts as prerequisite or reference manual steps.*

---

#### 1.8 Quick Start with Kind
**File:** `getting-started/quickstart-kind.md`

Document local development quick start using Kind:
- Prerequisites (Docker, Kind CLI)
- Create Kind cluster
- Install operator
- Deploy DocumentDB instance
- Connect and verify
- Cleanup

---

#### 1.9 Quick Start with K3s
**File:** `getting-started/quickstart-k3s.md`

Document lightweight quick start using K3s:
- Prerequisites
- Install K3s
- Install operator
- Deploy DocumentDB instance
- Connect and verify
- Cleanup

---

### Phase 2: Configuration Documentation

#### 2.1 Cluster Configuration Page
**File:** `configuration/cluster-configuration.md`

Document all DocumentDB cluster configuration options:
- Basic configuration (name, namespace, version, instances)
- Bootstrap options
- Affinity and tolerations
- Labels and annotations
- YAML examples for each option

---

#### 2.2 TLS Configuration Page
**File:** `configuration/tls.md`

Extract and expand TLS documentation from advanced-configuration:
- TLS mode overview (SelfSigned, Provided, CertManager)
- Detailed setup for each mode
- Certificate rotation
- Troubleshooting TLS issues
- Client connection with TLS

---

#### 2.3 Storage Configuration Page
**File:** `configuration/storage.md`

Document storage configuration:
- Storage class selection
- Size configuration
- Volume expansion procedures
- Provider-specific guidance (AKS, EKS, GKE)
- Performance considerations

---

#### 2.4 Networking Configuration Page
**File:** `configuration/networking.md`

Document networking setup:
- Service types (ClusterIP, LoadBalancer, NodePort)
- External access configuration
- DNS configuration
- Port reference
- Load balancer annotations by provider

---

#### 2.5 Resource Management Page
**File:** `configuration/resource-management.md`

Document resource configuration:
- CPU and memory requests/limits
- Sizing guidelines by workload type
- Vertical scaling procedures

---

### Phase 3: High Availability Documentation

#### 3.1 High Availability Overview
**File:** `high-availability/overview.md`

Document HA concepts:
- What is high availability in DocumentDB context
- Types of HA: Local HA, Multi-Region, Multi-Cloud
- When to use each type
- HA architecture diagram
- RTO/RPO concepts
- Trade-offs and considerations

---

#### 3.2 Local HA Page
**File:** `high-availability/local-ha.md`

Document local HA setup:
- Instance count configuration
- Pod anti-affinity setup
- Availability zone distribution
- Automatic failover behavior
- Manual failover using promote command

---

### Phase 4: Multi-Region Deployment Documentation

#### 4.1 Multi-Region Overview
**File:** `multi-region-deployment/overview.md`

Document multi-region concepts:
- What is multi-region deployment
- Use cases (DR, latency reduction, compliance)
- Architecture overview
- Network connectivity requirements
- RTO/RPO considerations

---

#### 4.2 Multi-Region Setup Guide
**File:** `multi-region-deployment/setup.md`

Document multi-region setup:
- Prerequisites and planning
- Step-by-step deployment guide
- Data replication configuration
- Reference AKS Fleet deployment examples in playground
- Verification steps

---

#### 4.3 Multi-Region Failover Procedures
**File:** `multi-region-deployment/failover-procedures.md`

Document failover procedures:
- Planned failover steps
- Unplanned failover runbook
- Verification and rollback

---

### Phase 5: Multi-Cloud Deployment Documentation

#### 5.1 Multi-Cloud Overview
**File:** `multi-cloud-deployment/overview.md`

Document multi-cloud concepts:
- What is multi-cloud deployment
- Use cases (vendor independence, compliance, DR)
- Architecture overview (AKS + GKE + EKS)
- Network connectivity (service mesh, VPN)

---

#### 5.2 Multi-Cloud Setup Guide
**File:** `multi-cloud-deployment/setup.md`

Document multi-cloud setup:
- Prerequisites and planning
- Step-by-step deployment guide
- Cross-cloud replication configuration
- Reference multi-cloud-deployment examples in playground
- Verification steps

---

#### 5.3 Multi-Cloud Failover Procedures
**File:** `multi-cloud-deployment/failover-procedures.md`

Document failover procedures:
- Cross-cloud failover runbook
- Recovery verification
- Rollback procedures

---

### Phase 6: Security Documentation

#### 6.1 Security Overview Page
**File:** `security/overview.md`

Document security model:
- Security layers (container, cluster, application)
- Security features overview
- Best practices summary

---

#### 6.2 RBAC Configuration Page
**File:** `security/rbac.md`

Document RBAC setup:
- Operator RBAC requirements
- Instance service account permissions
- Custom RBAC for users
- Namespace isolation

---

#### 6.3 Network Policies Page
**File:** `security/network-policies.md`

Document network security:
- Network policy examples
- Pod-to-pod communication
- External access control
- Ingress/egress rules

---

#### 6.4 Secrets Management Page
**File:** `security/secrets-management.md`

Document secrets handling:
- Auto-generated credentials
- Custom credential secrets
- External secrets integration (Key Vault, Vault)
- Secret rotation

---

### Phase 7: Operations Documentation

#### 7.1 Enhance Backup and Restore Page
**File:** `operations/backup-and-restore.md`

Enhance existing backup documentation:
- Add conceptual introduction
- Add troubleshooting section
- Add backup verification procedures
- Add emergency backup procedures

---

#### 7.2 Scaling Documentation
**File:** `operations/scaling.md`

Document scaling procedures:
- Horizontal scaling (add/remove instances)
- Vertical scaling (resize resources)
- Storage expansion
- Scaling considerations and impact

---

#### 7.3 Upgrades Documentation
**File:** `operations/upgrades.md`

Document upgrade procedures:
- Operator upgrades
- DocumentDB version upgrades
- Rolling update behavior
- Rollback procedures
- Version compatibility matrix

---

#### 7.4 Failover Documentation
**File:** `operations/failover.md`

Document failover procedures:
- Automatic failover behavior
- Manual failover (promote command)
- Failover testing
- Application considerations

---

#### 7.5 Restore Deleted Cluster
**File:** `operations/restore-deleted-cluster.md`

Document how to restore a deleted cluster:
- Prerequisites (existing backup)
- Step-by-step restoration process
- Verification steps
- Common pitfalls

---

#### 7.6 Maintenance Documentation
**File:** `operations/maintenance.md`

Document maintenance procedures:
- Node maintenance mode
- Rolling updates
- Draining nodes
- Scheduled maintenance windows

---

### Phase 8: Monitoring, Tuning & Troubleshooting

#### 8.1 Monitoring Overview Page
**File:** `monitoring/overview.md`

Document monitoring setup:
- Monitoring architecture
- Prometheus integration
- Key metrics to monitor
- Alert recommendations
- Reference telemetry playground examples

---

#### 8.2 Metrics Reference Page
**File:** `monitoring/metrics.md`

Document available metrics:
- Operator metrics
- Database metrics
- Gateway metrics
- Query examples

---

#### 8.3 Tuning Guide
**File:** `tuning/tuning-guide.md`

Document performance tuning:
- Resource sizing recommendations
- Storage performance optimization
- Connection pooling tuning
- Query optimization tips
- Benchmarking guidance

---

#### 8.4 Diagnostic Tools Page
**File:** `troubleshooting/diagnostic-tools.md`

Document diagnostic procedures:
- Viewing logs (kubectl, stern)
- Checking events
- Using kubectl-documentdb plugin for diagnostics
- Inspecting pod status
- Collecting diagnostic reports

---

#### 8.5 Common Issues Page
**File:** `troubleshooting/common-issues.md`

Document common issues and solutions (minimum 10):
- Pod stuck in Pending
- Connection refused
- TLS errors
- Backup failures
- Storage full
- Replication lag
- Certificate issues
- Network policy blocking

Each issue should include: symptoms, cause, solution, prevention tips.

---

### Phase 9: Reference & Community

#### 9.1 Labels and Annotations Reference
**File:** `reference/labels-annotations.md`

Document all labels and annotations:
- Standard Kubernetes labels
- DocumentDB-specific labels
- Useful annotations
- Usage examples

---

#### 9.2 Application Connection Guide
**File:** `getting-started/connecting-to-documentdb.md`

Document how to connect applications:
- Connection string format
- MongoDB drivers compatibility
- Connection pooling
- TLS connections
- Example code snippets (Python, Node.js, Go, Java)

---

#### 9.3 Release Notes Page
**File:** `release-notes.md`

Create release notes page:
- Link to or embed CHANGELOG.md
- Version compatibility matrix
- Deprecation notices

---

#### 9.4 Support Page
**File:** `support.md`

Create support and help page:
- How to get help (GitHub Discussions, Issues)
- Discord community link
- Community resources
- Links to documentation sections

---

#### 9.5 Privacy Page
**File:** `privacy.md`

Document data collection and privacy:
- What data the operator collects (if any)
- Telemetry information
- How to opt out
- Data retention policies

---

#### 9.6 Contributing Page
**File:** `contributing.md`

Link to contribution resources:
- Link to GitHub [CONTRIBUTING.md](https://github.com/microsoft/documentdb-kubernetes-operator/blob/main/CONTRIBUTING.md)
- Link to [ADOPTERS.md](https://github.com/microsoft/documentdb-kubernetes-operator/blob/main/ADOPTERS.md)
- Development setup overview
- How to submit PRs

---

#### 9.7 Security Reporting Page
**File:** `security-reporting.md`

Document how to report security issues:
- Security vulnerability reporting process
- Link to GitHub Security tab
- Responsible disclosure guidelines
- Link to [SECURITY.md](https://github.com/microsoft/documentdb-kubernetes-operator/blob/main/SECURITY.md)

---

### Phase 10: Final Review

#### 10.1 Navigation and Structure Implementation
Update mkdocs.yml to implement the new documentation structure with proper navigation.

---

#### 10.2 Cross-linking and Consistency Review
Review all documentation for consistent terminology, working cross-links, and consistent formatting.

---

#### 10.3 Technical Review
Technical review: verify accuracy of all procedures, test code examples, validate YAML configurations.

---

## Summary

| Phase | Focus Area | Tasks |
|-------|------------|-------|
| Phase 1 | Foundation | 9 |
| Phase 2 | Configuration | 5 |
| Phase 3 | High Availability | 2 |
| Phase 4 | Multi-Region Deployment | 3 |
| Phase 5 | Multi-Cloud Deployment | 3 |
| Phase 6 | Security | 4 |
| Phase 7 | Operations | 6 |
| Phase 8 | Monitoring, Tuning & Troubleshooting | 5 |
| Phase 9 | Reference & Community | 7 |
| Phase 10 | Final Review | 3 |
| **Total** | | **47** |

---

## Documentation Guidelines

### Scripts and Examples Pattern

**Keep scripts in `documentdb-playground/` and reference from docs.** Do not embed full scripts in documentation.

**Documentation should contain:**
- Overview and prerequisites
- Quick start command referencing playground scripts
- Key configuration explained with small snippets
- Step-by-step manual alternative for learning
- Verification and troubleshooting

**Example structure for cloud deployment guides:**

```markdown
## Quick Start

For automated deployment, use the playground scripts:

\`\`\`bash
cd documentdb-playground/aks-setup/scripts
./create-cluster.sh --deploy-instance
\`\`\`

See [AKS Setup Scripts](../../documentdb-playground/aks-setup/README.md) for options.

## Understanding the Configuration

### Storage Class
\`\`\`yaml
storageClassName: managed-csi  # Azure StandardSSD_LRS
\`\`\`
```

**Benefits:**
- Single source of truth for scripts
- Scripts stay tested and maintained in one place
- Documentation explains *what* and *why*
- Playground contains *executable how*
