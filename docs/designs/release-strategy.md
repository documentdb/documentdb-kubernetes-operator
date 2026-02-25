# DocumentDB Kubernetes Operator Release Strategy

This document outlines the release strategy, support policy, and versioning scheme for the DocumentDB Kubernetes Operator project.

## Table of Contents

- [Overview](#overview)
- [Versioning Scheme](#versioning-scheme)
- [Release Cadence](#release-cadence)
- [Release Types](#release-types)
- [Support Policy](#support-policy)
- [Compatibility Matrix](#compatibility-matrix)
- [Release Process](#release-process)

---

## Overview

The DocumentDB Kubernetes Operator follows a time-based release schedule with semantic versioning. We aim to balance stability with the timely delivery of new features and bug fixes.

**Key Principles:**
- Predictable release schedule for users to plan upgrades
- Clear support windows for production deployments
- Backward compatibility within minor versions
- Roll-forward strategy for bug and security fixes
- Easy to identify which versions of underlying components are used

---

## Versioning Scheme

We follow [Semantic Versioning 2.0.0](https://semver.org/):

```
<major>.<minor>.<patch>[-<pre-release>]
```

| Component | Description | Example |
|-----------|-------------|---------|
| `major` | Breaking API/CRD changes | `1.0.0` → `2.0.0` |
| `minor` | New features, backward-compatible | `0.1.0` → `0.2.0` |
| `patch` | Bug fixes, security patches | `0.1.0` → `0.1.1` |
| `pre-release` | Release candidates | `0.2.0-rc.1` |

**Git Tag Format:** `v<major>.<minor>.<patch>` (e.g., `v0.2.0`)

### Version Stages

| Stage | Version Pattern | Description |
|-------|-----------------|-------------|
| Preview | `0.x.x` | API may change, not recommended for production |
| Stable | `1.x.x+` | Stable API, production-ready |

---

## Release Cadence

| Release Type | Frequency | Description |
|--------------|-----------|-------------|
| **Minor Release** | Every 3 months | New features, enhancements, dependency updates |
| **Patch Release** | As needed | Bug fixes, security patches |
| **Security Patch** | ASAP after CVE disclosure | Security fixes only |
| **Release Candidate** | 1-2 weeks before minor release | Preview testing |

**Dependency and Component Updates:**
- Updates to CNPG, PostgreSQL, DocumentDB extension, and gateway versions are introduced in **minor releases**
- Patch releases do not include dependency version changes unless required for security fixes

### Release Schedule (Planned)

> **Note:** Dates are approximate and subject to change. We aim to align releases with major industry events. Updates will be communicated in [GitHub Discussions](https://github.com/documentdb/documentdb-kubernetes-operator/discussions).

| Version | Target Date | Event Alignment | Feature Freeze |
|---------|-------------|-----------------|----------------|
| v0.2.0 | Mar 2026 | KubeCon EU | Early Mar 2026 |
| v0.3.0 | May 2026 | Microsoft Build | Early May 2026 |
| v0.4.0 | Aug 2026 | Pre-conference prep | Early Aug 2026 |
| v0.5.0 | Nov 2026 | Microsoft Ignite / KubeCon NA | Early Nov 2026 |

---

## Release Types

### Development Build
- **Support:** None (experimental)
- **Source:** Built from `main` branch on every merge
- **Use Case:** Testing latest features, not for production

### Release Candidate (RC)
- **Support:** None (preview)
- **Format:** `v0.x.0-rc.N`
- **Duration:** 1-2 weeks before final release
- **Use Case:** Community testing before final release

### Minor Release
- **Support:** Until 3 months after next minor release
- **Format:** `v0.x.0`
- **Content:** New features, enhancements, bug fixes

### Patch Release
- **Support:** Same as corresponding minor release
- **Format:** `v0.x.y` (where y > 0)
- **Content:** Bug fixes, backward-compatible changes only

### Security Patch
- **Support:** Same as corresponding minor release
- **Urgency:** Released ASAP after vulnerability disclosure
- **Content:** Security fix only, minimal code changes

---

## Support Policy

### Support Window

Each minor release is supported until **3 months after the next minor release** is published. This provides approximately **6 months of total support** per release.

```
v0.1.0 released -----> v0.2.0 released -----> v0.1.x EOL (3 months later)
       |                     |                      |
       |<--- Active Support -|--- Extended Support -->|
       |       (3 months)    |      (3 months)       |
```

### Support Status Table

| Version | Release Date | End of Life | Status |
|---------|--------------|-------------|--------|
| v0.1.x | Dec 2025 | 3 months after v0.2.0 | Supported |
| main | N/A | N/A | Development only |

### What "Support" Means

**Technical Support:**
- Community support via [Discord](https://discordapp.com/channels/1374170121219866635/1435045191156236458) and [GitHub Discussions](https://github.com/documentdb/documentdb-kubernetes-operator/discussions)
- Issue tracking and triage
- Best-effort response (no SLA for community version)

**Bug Fixes:**
- All bug fixes are included in the next release (roll forward)
- Users should upgrade to the latest version to receive fixes

**Security Fixes:**
- Security fixes are prioritized and included in the next release
- For critical vulnerabilities, an expedited patch release may be issued
- Security advisories published for critical vulnerabilities

### Roll Forward Policy

We follow a **roll forward** strategy rather than backporting:

- **No backports:** Fixes are not backported to older releases
- **Upgrade path:** Users should upgrade to the latest release to receive bug fixes and security patches
- **Rapid releases:** Critical issues trigger expedited patch releases on the current version

**Why roll forward?**
1. Reduces maintenance complexity
2. Ensures users benefit from all improvements
3. Simplifies testing and validation
4. Encourages staying current with releases

**For critical security issues:**
- We may issue an expedited patch release (e.g., `0.1.4` → `0.1.5`)
- Users on older versions should upgrade to the latest release

---

## Compatibility Matrix

### Kubernetes Versions

| Operator Version | Minimum K8s | Maximum K8s | Tested Versions |
|------------------|-------------|-------------|-----------------|
| v0.1.x | 1.28 | 1.32 | 1.30, 1.31, 1.32 |

> **Policy:** We support Kubernetes versions that are within the [Kubernetes support window](https://kubernetes.io/releases/) at the time of the operator release.

### Dependency Versions

| Operator Version | CloudNative-PG | cert-manager | Helm |
|------------------|----------------|--------------|------|
| v0.1.x | 1.28.0 | 1.19.2+ | 3.x |

### DocumentDB Component Versions

| Operator Version | PostgreSQL | DocumentDB Extension | DocumentDB Gateway |
|------------------|------------|----------------------|--------------------|
| v0.1.x | 16.x | 1.x | 1.x |

> **Note:** The operator bundles compatible versions of PostgreSQL, the DocumentDB Postgres extension (`pg_documentdb`), and the DocumentDB Gateway. Image tags follow the format `<postgres-version>-v<extension-version>` (e.g., `16.3-v1.3.0`). Our goal is to use CNPG [Image Catalog](https://cloudnative-pg.io/documentation/current/image_catalog/) to manage these versions in the future.

### Dependency Versioning Policy

**CloudNative-PG (CNPG):**
- We only bundle **stable releases** of CloudNative-PG
- CNPG version is updated when a new stable release provides required features or critical fixes
- CNPG upgrades are tested before being included in an operator release

**DocumentDB Components (PostgreSQL, Extension, Gateway):**
- We bundle **stable, tested versions** of DocumentDB components
- The operator is validated against specific version combinations before release
- We do not automatically track the latest DocumentDB releases; we deliberately select and test compatible versions
- Users cannot mix arbitrary versions—the operator manages compatible combinations

### Container Image Support

| Architecture | Supported |
|--------------|-----------|
| linux/amd64 | ✅ |
| linux/arm64 | ✅ |

> **Note:** Additional architectures (e.g., ppc64le, s390x) may be added based on community interest. Please open a [GitHub Issue](https://github.com/documentdb/documentdb-kubernetes-operator/issues) to request support for other platforms.

---

## Release Process

For detailed release instructions, including how to use the release agent and step-by-step procedures, see [RELEASE.md](../../RELEASE.md).

---

*Last Updated: February 2026*
