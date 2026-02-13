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
│   ├── quickstart.md                     # Current preview/index.md
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
│   └── maintenance.md                    # Node maintenance, rolling updates
├── high-availability/
│   ├── overview.md                       # HA concepts and setup
│   └── multi-instance-clusters.md        # Local HA configuration
├── disaster-recovery/
│   ├── overview.md                       # DR planning and concepts
│   ├── multi-region.md                   # Cross-region deployment
│   ├── multi-cloud.md                    # Cross-cloud deployment
│   └── recovery-procedures.md            # Recovery runbooks
├── security/
│   ├── overview.md                       # Security model
│   ├── rbac.md                           # RBAC configuration
│   ├── network-policies.md               # Network security
│   └── secrets-management.md             # Credentials and secrets
├── monitoring/
│   ├── overview.md                       # Monitoring setup
│   └── metrics.md                        # Prometheus metrics reference
├── troubleshooting/
│   ├── diagnostic-tools.md               # Logs, events, debugging
│   └── common-issues.md                  # Known issues and solutions
├── reference/
│   ├── api-reference.md                  # CRD documentation
│   ├── kubectl-plugin.md                 # Existing plugin docs
│   └── labels-annotations.md             # Labels/annotations reference
├── faq.md                                # Expanded FAQ
└── release-notes.md                      # Version history
```

---

## Tasks

### Phase 1: Foundation

#### 1.1 Terminology and Prerequisites Page
**File:** `getting-started/before-you-start.md`

Create a terminology page covering:
- Kubernetes concepts (Pod, Service, PVC, StorageClass, Namespace, CRD, Operator)
- DocumentDB concepts (Instance, Primary, Replica, Gateway, Cluster)
- MongoDB compatibility concepts
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
- How does DocumentDB differ from MongoDB?
- Can I run DocumentDB in production on Kubernetes?
- What Kubernetes distributions are supported?
- How do I connect my application?
- What happens during a failover?
- How do I scale my cluster?
- How do backups work?
- How do I enable TLS?
- What are the resource requirements?
- How do I migrate from existing MongoDB?

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
- HA architecture diagram
- RTO/RPO concepts
- Trade-offs and considerations

---

#### 3.2 Multi-Instance Clusters Page
**File:** `high-availability/multi-instance-clusters.md`

Document local HA setup:
- Instance count configuration
- Pod anti-affinity setup
- Availability zone distribution
- Automatic failover behavior
- Manual failover using promote command

---

### Phase 4: Disaster Recovery Documentation

#### 4.1 Disaster Recovery Overview
**File:** `disaster-recovery/overview.md`

Document DR concepts:
- Difference between HA and DR
- DR planning considerations
- Backup strategies for DR
- RTO/RPO planning guidance

---

#### 4.2 Multi-Region Deployment Page
**File:** `disaster-recovery/multi-region.md`

Document multi-region deployment:
- Architecture for multi-region
- Network connectivity requirements
- Data replication options
- Failover procedures
- Reference AKS Fleet deployment examples in playground

---

#### 4.3 Multi-Cloud Deployment Page
**File:** `disaster-recovery/multi-cloud.md`

Document multi-cloud deployment:
- Architecture for multi-cloud (AKS + GKE + EKS)
- Network connectivity (service mesh, VPN)
- Cross-cloud replication
- Reference multi-cloud-deployment examples in playground

---

#### 4.4 Recovery Procedures Page
**File:** `disaster-recovery/recovery-procedures.md`

Document recovery procedures:
- Point-in-time recovery (PITR)
- Full cluster recovery from backup
- Cross-region failover runbook
- Verification and rollback procedures

---

### Phase 5: Security Documentation

#### 5.1 Security Overview Page
**File:** `security/overview.md`

Document security model:
- Security layers (container, cluster, application)
- Security features overview
- Best practices summary

---

#### 5.2 RBAC Configuration Page
**File:** `security/rbac.md`

Document RBAC setup:
- Operator RBAC requirements
- Instance service account permissions
- Custom RBAC for users
- Namespace isolation

---

#### 5.3 Network Policies Page
**File:** `security/network-policies.md`

Document network security:
- Network policy examples
- Pod-to-pod communication
- External access control
- Ingress/egress rules

---

#### 5.4 Secrets Management Page
**File:** `security/secrets-management.md`

Document secrets handling:
- Auto-generated credentials
- Custom credential secrets
- External secrets integration (Key Vault, Vault)
- Secret rotation

---

### Phase 6: Operations Documentation

#### 6.1 Enhance Backup and Restore Page
**File:** `operations/backup-and-restore.md`

Enhance existing backup documentation:
- Add conceptual introduction
- Add troubleshooting section
- Add backup verification procedures
- Add emergency backup procedures

---

#### 6.2 Scaling Documentation
**File:** `operations/scaling.md`

Document scaling procedures:
- Horizontal scaling (add/remove instances)
- Vertical scaling (resize resources)
- Storage expansion
- Scaling considerations and impact

---

#### 6.3 Upgrades Documentation
**File:** `operations/upgrades.md`

Document upgrade procedures:
- Operator upgrades
- DocumentDB version upgrades
- Rolling update behavior
- Rollback procedures
- Version compatibility matrix

---

#### 6.4 Failover Documentation
**File:** `operations/failover.md`

Document failover procedures:
- Automatic failover behavior
- Manual failover (promote command)
- Failover testing
- Application considerations

---

#### 6.5 Maintenance Documentation
**File:** `operations/maintenance.md`

Document maintenance procedures:
- Node maintenance mode
- Rolling updates
- Draining nodes
- Scheduled maintenance windows

---

### Phase 7: Monitoring & Troubleshooting

#### 7.1 Monitoring Overview Page
**File:** `monitoring/overview.md`

Document monitoring setup:
- Monitoring architecture
- Prometheus integration
- Key metrics to monitor
- Alert recommendations
- Reference telemetry playground examples

---

#### 7.2 Metrics Reference Page
**File:** `monitoring/metrics.md`

Document available metrics:
- Operator metrics
- Database metrics
- Gateway metrics
- Query examples

---

#### 7.3 Diagnostic Tools Page
**File:** `troubleshooting/diagnostic-tools.md`

Document diagnostic procedures:
- Viewing logs (kubectl, stern)
- Checking events
- Using kubectl-documentdb plugin for diagnostics
- Inspecting pod status
- Collecting diagnostic reports

---

#### 7.4 Common Issues Page
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

### Phase 8: Reference & Examples

#### 8.1 Labels and Annotations Reference
**File:** `reference/labels-annotations.md`

Document all labels and annotations:
- Standard Kubernetes labels
- DocumentDB-specific labels
- Useful annotations
- Usage examples

---

#### 8.2 Application Connection Guide
**File:** `getting-started/connecting-to-documentdb.md`

Document how to connect applications:
- Connection string format
- MongoDB drivers compatibility
- Connection pooling
- TLS connections
- Example code snippets (Python, Node.js, Go, Java)

---

#### 8.3 Release Notes Page
**File:** `release-notes.md`

Create release notes page:
- Link to or embed CHANGELOG.md
- Version compatibility matrix
- Deprecation notices

---

### Phase 9: Final Review

#### 9.1 Navigation and Structure Implementation
Update mkdocs.yml to implement the new documentation structure with proper navigation.

---

#### 9.2 Cross-linking and Consistency Review
Review all documentation for consistent terminology, working cross-links, and consistent formatting.

---

#### 9.3 Technical Review
Technical review: verify accuracy of all procedures, test code examples, validate YAML configurations.

---

## Summary

| Phase | Focus Area | Tasks |
|-------|------------|-------|
| Phase 1 | Foundation | 7 |
| Phase 2 | Configuration | 5 |
| Phase 3 | High Availability | 2 |
| Phase 4 | Disaster Recovery | 4 |
| Phase 5 | Security | 4 |
| Phase 6 | Operations | 5 |
| Phase 7 | Monitoring & Troubleshooting | 4 |
| Phase 8 | Reference & Examples | 3 |
| Phase 9 | Final Review | 3 |
| **Total** | | **37** |

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
