# Combined-Image Fallback for Clusters Without ImageVolume

## Summary

The operator mounts the DocumentDB extension into PostgreSQL pods with the
Kubernetes [ImageVolume](https://kubernetes.io/docs/concepts/storage/volumes/#image)
feature. That needs **both**:

- an API server with the `ImageVolume` feature gate on (GA/on by default in
  Kubernetes 1.35, beta/off by default in 1.33 and 1.34), and
- a node container runtime that implements image volumes (containerd ≥ 2.1 or
  CRI-O ≥ 1.33).

Many production clusters (e.g. Kubernetes 1.33/1.34 managed offerings, or any
cluster still on containerd 1.7) do not meet both requirements. Today the
validating webhook rejects `DocumentDB` creation on those clusters, and a
cluster whose API server accepts image volumes but whose runtime does not (for
example containerd 1.7) passes the webhook and then hangs.

This design adds a **combined-image fallback**: when ImageVolume is not usable,
the operator runs PostgreSQL from a single image that already contains the
DocumentDB extension, so no image volume is needed.

## History

| Commit | Change |
|--------|--------|
| `47acc35` (#208) | Introduced ImageVolume mode next to a combined (`documentdb-local:16`) fallback chosen by Kubernetes version. |
| `d4e5e9a` (#275) | Removed the combined fallback and required Kubernetes 1.35+. |
| `e569542` (#437) | Replaced the version gate with a webhook capability probe that rejects creation when ImageVolume is unavailable; lowered the floor to 1.33. |

The old fallback used a PostgreSQL 16 image with its own UID/GID and selected
the mode once at operator start-up from the Kubernetes version only, which did
not account for the container runtime.

## Goals

- `DocumentDB` clusters work on clusters without ImageVolume (feature gate off,
  older Kubernetes, or containerd < 2.1), with no change to the custom resource.
- ImageVolume stays the preferred mode wherever it works.
- A cluster never switches modes after creation.
- Operators can override the automatic choice.

## Non-goals

- Migrating an existing cluster between modes.
- Supporting PostgreSQL majors other than 18.
- Change-stream preview images in combined mode.

## Design

### Image modes

| Mode | CNPG `spec.imageName` | CNPG `spec.postgresql.extensions` |
|------|-----------------------|-----------------------------------|
| `imageVolume` | upstream CNPG PostgreSQL 18 image (`spec.image.postgres`) | `documentdb` extension image mounted via `ImageVolumeSource` |
| `combined` | combined image (PostgreSQL 18 + DocumentDB) | *(empty)* |

The operator-level setting `DOCUMENTDB_IMAGE_MODE` (Helm `imageMode`) accepts:

- `auto` (default): detect per new cluster (see below).
- `imageVolume`: always use ImageVolume. The webhook keeps rejecting creation
  when the API server does not accept image volumes (current behaviour).
- `combined`: always use the combined image.

### Detection (`auto`)

Detection runs **once, when the CNPG `Cluster` is first created**:

1. **Node runtimes.** List nodes (uncached) and parse
   `status.nodeInfo.containerRuntimeVersion` of every schedulable Linux node
   that already reports a runtime (`spec.unschedulable` false; Windows nodes
   and nodes still registering are skipped). Every such node must report
   containerd ≥ 2.1 or CRI-O ≥ 1.33; any other runtime, an unparsable version,
   or a `Forbidden` list error selects `combined`. When no node qualifies
   (scale-to-zero pools, all nodes cordoned during an upgrade), the runtime is
   not judged and the API server decides alone; the event reason says so.
2. **API server.** Run the existing differential server-side dry-run probe
   (a baseline `emptyDir` pod vs. an `image` volume pod). The probe pods carry a
   restricted-profile security context and minimal CPU/memory requests and
   limits, so Pod Security Admission and quotas or policies that require limits
   do not make the probe inconclusive. Only a definitive 4xx rejection counts;
   timeouts, throttling, `401`, server and transport errors are retried.
   - supported → `imageVolume`
   - unsupported → `combined`
   - inconclusive (quota, another webhook) → `imageVolume` when the server
     version is ≥ 1.35 (ImageVolume GA), otherwise `combined`.

`combined` is the safe default: it works on every runtime. Because the mode is
permanent, transient failures (a node list error other than `Forbidden`, a
retryable probe error, or an unreadable server version after an inconclusive
probe) do not pick a mode: the operator requeues and retries before creating
the CNPG cluster.

### Mode persistence

The created CNPG cluster is annotated `documentdb.io/image-mode=<mode>`, and
that annotation is the source of truth for **existing** clusters: only a cluster
annotated `combined` (or, if the annotation was removed, one that mounts no
extension and runs an image from the combined repository) is rendered in
combined mode. Clusters without the
annotation (created by earlier operator versions) keep the ImageVolume
rendering, so the operator never rewrites their `imageName` — in particular the
0.1.x `documentdb-local:16` clusters, which have no extension entry, are not
mistaken for combined clusters. Changing `imageMode` or upgrading the container
runtime only affects new clusters.

The operator also emits a `Normal` `ImageModeSelected` event on the
`DocumentDB` with the detection reason, so users can see why a mode was picked.
(This required wiring an event recorder into the DocumentDB controller, which
also makes its existing events visible.)

### Combined image

`ghcr.io/documentdb/documentdb-kubernetes-operator/documentdb-combined:18-<version>`

- Built from `.github/dockerfiles/Dockerfile_combined`: the CNPG
  `postgresql:18-minimal-trixie` base plus the same packages as the extension
  image (`pg_cron`, `pgvector`, `postgis`, and the pinned DocumentDB `.deb`).
- Combined images exist from DocumentDB `1.0.0` on; clusters pinned to an older
  `documentDBVersion` have no combined image.
- Runs as the CNPG `postgres` user (UID 26), so no UID/GID override is needed.
- The tag starts with the PostgreSQL major because CloudNativePG derives the
  major version from the tag (`^(\d\.?)+`); a bare `1.0.0` would read as
  PostgreSQL 1.

**Tag resolution.** The operator reuses the extension version so both modes run
the same DocumentDB build: `<combined repo>:18-<v>`, where `<v>` is the tag of
`spec.image.documentDB` when that tag names a release (`X.Y…`) and the
reference has no digest, else `spec.documentDBVersion`, else the
`DOCUMENTDB_VERSION` env (Helm `documentDbVersion`), else the compiled default.
Digest references (with or without a tag), `latest`, and the change-stream
images therefore never produce a nonexistent combined tag. Admission resolves
the binary version with the same precedence (both use
`util.ReleaseVersionTag`), so the `schemaVersion` and image-rollback checks
validate the version the combined image actually runs. Only the version is taken from `spec.image.documentDB`; the
repository defaults to the GHCR path above and can be overridden with
`DOCUMENTDB_COMBINED_IMAGE_REPO` (Helm `image.documentdbCombined.repository`).
`DOCUMENTDB_IMAGE_PULL_POLICY` also applies to the combined image through CNPG
`spec.imagePullPolicy`.

Because the tag tracks the extension version, the version-based admission check
(`schemaVersion` ≤ binary version) and the release version-bump automation work
unchanged.

### Upgrades in combined mode

Changing `documentDBVersion` (or the extension image tag) changes the combined
image. The existing sync patches CNPG `spec.imageName`, CNPG performs a rolling
update (switchover for the primary), and the existing `ALTER EXTENSION`
reconcile runs once the primary is healthy. `status.documentDBImage` reports the
combined image.

### Webhook

- `auto` and `combined`: the ImageVolume probe no longer blocks creation.
- `imageVolume`: unchanged — creation is rejected when the API server does not
  accept image volumes.

### RBAC and Helm

- The operator ClusterRole gains `get`/`list` on `nodes`.
- The chart `kubeVersion` floor drops to `>= 1.32.0-0`, the oldest version tested
  with the combined image. CloudNativePG tests 1.31 and 1.32 but officially
  supports only 1.33+.
- New values: `imageMode` and `image.documentdbCombined.repository`.

## Pipelines

- `build_documentdb_images.yml`: a third matrix image `documentdb-combined`
  built from the same pinned and checksum-verified `.deb` (for RC/source builds,
  the same pinned source and package version, compiled per image job), pushed
  as `18-<candidate>-<arch>`, then merged, signed, and verified as
  `18-<candidate>`.
- `release_documentdb_images.yml`: promotes `documentdb-combined:18-<candidate>`
  to `18-<version>`. The version-bump PR needs no extra edits because the
  combined tag is derived from `DEFAULT_DOCUMENTDB_TAG`.
- `release_operator.yml`: a `verify-combined-image` job anonymously inspects
  `documentdb-combined:18-<documentDbVersion>` before promoting operator images,
  so an operator release fails while the combined image for its default
  version is missing or private (`force` bypasses it).

## Testing

- Unit tests: runtime-version parsing, node and probe decision table, mode
  derivation from an existing cluster, combined rendering, sync/status with no
  extension, webhook behaviour per mode.
- Helm unit tests: env wiring and RBAC.
- Local kind verification:

  | Kubernetes | containerd | ImageVolume gate | `imageMode` | Result |
  |------------|------------|------------------|-------------|--------|
  | 1.35.0 | 2.2.0 | default (on) | `auto` | `imageVolume` |
  | 1.33.4 | 2.1.3 | on | `auto` | `imageVolume` |
  | 1.33.4 | 2.1.3 | off | `auto` | `combined` (API probe) |
  | 1.32.5 | 2.1.1 | default (off) | `auto` | `combined` (API probe) |
  | 1.32.0 | 1.7.24 | default (off) | `auto` | `combined` (runtime) |
  | 1.33.4 | 1.7.24 ¹ | on | `auto` | `combined` (runtime) |
  | 1.35.0 | 1.7.24 ¹ | default (on) | `auto` | `combined` (runtime) |
  | 1.33.4 | 2.1.3 | off | `imageVolume` | rejected by the webhook |
  | 1.35.0 | 2.2.0 | default (on) | `combined` | `combined` |

  ¹ kind node image with the containerd 1.7.24 binaries from
  `kindest/node:v1.32.0`.

  Each run checks the cluster becomes healthy, that the extension is at the
  expected version, and that it serves Mongo wire-protocol reads/writes. On
  1.32 with containerd 1.7 the e2e `lifecycle`, `status` and `data` suites also
  pass in combined mode, and an earlier run upgraded the combined image and
  verified the rolling update and `ALTER EXTENSION`.
- CI: the `imagevolume-block` job forces `imageMode=imageVolume` so it keeps
  covering the rejection path, and `imagevolume-preflight` asserts that the
  ImageVolume layout (annotation and extension entry) was selected.

## Limitations and follow-ups

- The combined image freezes its PostgreSQL 18 minor version and Debian base at
  build time, while ImageVolume clusters follow the floating CNPG base tag.
  Combined clusters pick up PostgreSQL and OS patches with the next DocumentDB
  release (or a rebuild of the combined image).
- Downgrading the operator below the release that introduces combined mode is
  not supported for combined clusters: older operators render the ImageVolume
  layout and their CNPG sync fails on the missing extension.
- Clusters that an earlier operator created on a runtime without image volume
  support (for example containerd 1.7 with the API gate on) keep the ImageVolume
  layout and stay unhealthy; recreate them to get combined mode.

- Mixed node pools: one unsupported schedulable node selects `combined` for the
  whole new cluster; detection does not consider the cluster's affinity.
- No in-place migration from `combined` to `imageVolume`.
- `spec.image.postgres` is ignored in combined mode (PostgreSQL ships inside the
  combined image), as are the repository and digest of `spec.image.documentDB`
  (only a release tag of a reference without a digest is reused); mirror the combined image through
  `image.documentdbCombined.repository`.
- The change-stream feature gate has no combined image; in combined mode the
  regular combined image for the cluster version is used.
- Cross-region replication between a combined cluster and an ImageVolume
  cluster should work (same PostgreSQL major and extension version) but is not
  yet tested.
- Add a CI e2e job for combined mode once the combined image is built in the
  e2e build stage.
