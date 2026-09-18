# DocumentDB Kubernetes Operator Release Strategy

This document outlines the release strategy, support policy, and versioning scheme for the DocumentDB Kubernetes Operator project.

## Table of Contents

- [Overview](#overview)
- [Release Cadence](#release-cadence)
- [Release Types](#release-types)
- [Branching and Tagging](#branching-and-tagging)
- [Support Policy](#support-policy)
- [Compatibility Matrix](#compatibility-matrix)
- [Upgrade Policy](#upgrade-policy)
- [Release Process](#release-process)

---

## Overview

The DocumentDB Kubernetes Operator follows the versioning and support model
established by [DocumentDB RFC-0008][documentdb-rfc]. The operator uses
time-based major releases so that its lifecycle and compatibility guarantees
are predictable alongside DocumentDB.

**Key Principles:**

- Predictable release schedule for users to plan upgrades
- One new major version each calendar year after `1.0.0`
- Clear support windows for production deployments
- Backward compatibility within minor versions
- Roll-forward on active development line and between major LTS versions
- Security backports to supported release lines
- Bug-fix backports on a case-by-case basis
- Easy to identify which versions of underlying components are used

---

## Release Cadence

| Release Type | Frequency | Description |
| ------------ | --------- | ----------- |
| **Major Release** | Once per calendar year after `1.0.0` | Starts a new supported line and may include breaking changes |
| **Minor Release** | As needed on the current development line | May include new features and breaking changes |
| **Patch Release** | As needed | Bug fixes, security patches |
| **Security Patch** | As soon as practical after a qualifying disclosure | Security fixes and only required supporting changes |
| **Release Candidate** | Before a major or other significant release | Preview testing; upgrades from RC builds are not supported |

**Dependency and Component Updates:**

- Updates to CNPG, PostgreSQL, the DocumentDB extension, and the gateway are introduced in minor releases on the current development line and may be breaking
- Breaking updates are not backported to supported release branches
- Patch releases include dependency changes only when required for security or critical stability

Release dates and feature freezes are communicated in [GitHub Discussions](https://github.com/documentdb/documentdb-kubernetes-operator/discussions). Preview `0.x` releases may use a more frequent cadence while the project prepares for `1.0.0`; those releases do not carry the stable-major compatibility guarantee.

---

## Release Types

### Development Build
- **Support:** None (experimental)
- **Source:** Built from `main` branch on every merge
- **Use Case:** Testing latest features, not for production

### Release Candidate (RC)

- **Support:** None (preview)
- **Format:** `vX.Y.Z-rc.N`
- **Duration:** 1-2 weeks before final release
- **Use Case:** Community testing before final release

### Minor Release

- **Support:** On the current development line, until the next minor release
- **Format:** `vX.Y.0`
- **Content:** New features, enhancements, bug fixes, and breaking changes

### Major Release

- **Support:** Until three months after the next major release is published
- **Format:** `vX.0.0` for the first release on the line
- **Content:** The accumulated development line, including any announced breaking changes

### Patch Release

- **Support:** Same as the corresponding release line
- **Format:** `vX.Y.Z` (where Z > 0)
- **Content:** Targeted bug fixes and security or critical stability updates

### Security Patch

- **Support:** Same as the corresponding release line
- **Urgency:** Released ASAP after vulnerability disclosure
- **Content:** Security fix only, minimal code changes

---

## Branching and Tagging

The unreleased next major version is developed on `main`. Current and previous supported major versions are maintained on `release/v#` branches. A release branch for the next major is created when release-candidate stabilization begins.

- Security fixes are backported to supported release branches.
- Bug fixes are backported on a case-by-case basis.
- New features and other minor changes remain on the current development line.
- Releases are tagged `v<major>.<minor>.<patch>`; release candidates add an `-rc.N` suffix.

For example, while `release/v1` is supported, `main` contains development for v2. A `release/v2` branch is created for the v2 release candidate. After v2 is released, `release/v1` remains supported during its three-month grace period.

---

## Support Policy

### Support Window

Each stable major release is supported until **three months after the next major release** is published. During that grace period, both the new and previous major release lines are supported.

Each minor release on the current development line is supported until the next minor release is published. Fixes during active development generally roll forward into the latest minor release instead of being backported to older development minors.

```
v1 released ----------------> v2 released ----------> v1 EOL
       active support              3-month grace period
```

### Support Status Table

| Version | Release Date | End of Life | Status |
|---------|--------------|-------------|--------|
| v0.1.x | Dec 2025 | When superseded by the next preview minor | Preview support |
| main | N/A | N/A | Development only |

### What "Support" Means

**Technical Support:**
- Community support via [Discord](https://discordapp.com/channels/1374170121219866635/1435045191156236458) and [GitHub Discussions](https://github.com/documentdb/documentdb-kubernetes-operator/discussions)
- Issue tracking and triage
- Best-effort response (no SLA for community version)

**Bug Fixes:**
- All bug fixes are included in the next release (roll forward)
- Bug fixes may be backported to supported release branches on a case-by-case basis
- Users should run the latest patch of a supported release line

**Security Fixes:**
- Security fixes are prioritized and backported to supported release branches
- Critical vulnerabilities may trigger an expedited patch release
- CVE fixes for releases outside the support window may be provided on a case-by-case basis
- Security advisories published for critical vulnerabilities

### Roll Forward Policy

We prefer **roll forward** during active development while servicing supported stable release lines:

- **Development minors:** Fixes generally roll forward to the latest minor release
- **Supported release branches:** Security fixes are backported; bug fixes are considered case by case
- **Out-of-support releases:** CVE fixes may be provided case by case, but users should upgrade to a supported release
- **Rapid releases:** Critical issues may trigger expedited patch releases

**Why roll forward?**
This keeps development simple and facilitates supporting stable releases.

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

Each release documents compatible versions of important bundled and dependent components. A stable major release line retains the Kubernetes and component compatibility promised when that major version is introduced for its entire support window.

**CloudNative-PG (CNPG):**
- We only bundle **stable releases** of CloudNative-PG
- CNPG version is updated when a new stable release provides required features or critical fixes
- CNPG upgrades are tested before being included in an operator release

**DocumentDB Components (PostgreSQL, Extension, Gateway):**
- We bundle **stable, tested versions** of DocumentDB components
- The operator is validated against specific version combinations before release
- We do not automatically track the latest DocumentDB releases; we deliberately select and test compatible versions
- Users cannot mix arbitrary versions—the operator manages compatible combinations

Development minor releases may remove support for Kubernetes, PostgreSQL, or
other component versions. These changes are not applied to supported release
branches and must be documented in the release notes and compatibility matrix.

### Container Image Support

| Architecture | Supported |
|--------------|-----------|
| linux/amd64 | ✅ |
| linux/arm64 | ✅ |

> **Note:** Additional architectures (e.g., ppc64le, s390x) may be added based on community interest. Please open a [GitHub Issue](https://github.com/documentdb/documentdb-kubernetes-operator/issues) to request support for other platforms.

---

## Upgrade Policy

Stable releases support direct in-place upgrades between consecutive major versions without requiring users to install every intermediate minor version. Fresh installation artifacts remain available for each supported major version.

Upgrades from release candidates are not supported. RC deployments must be replaced with or restored into a final release deployment according to the release-specific upgrade guidance.

---

## Release Process

For detailed release instructions, including how to use the release agent and step-by-step procedures, see [RELEASE.md](../../RELEASE.md).

---

[documentdb-rfc]: https://github.com/alaye-ms/documentdb/blob/2f898c561c3b3ef84895cd366248799bda82138d/rfcs/0008-versioning-and-support.md

*Last Updated: September 2026*
