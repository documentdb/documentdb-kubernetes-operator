# Upstream Release Dispatch Sender (Draft / Reference)

> **Status:** Reference draft. This workflow does **not** live in this repository —
> it must be added to the upstream **`documentdb/documentdb`** repository. It is
> provided here so the upstream maintainers (e.g. Guanzhou) can wire DocumentDB
> releases to this operator's image automation.

## What it does

When `documentdb/documentdb` publishes a GitHub release, this workflow sends a
[`repository_dispatch`](https://docs.github.com/en/actions/using-workflows/events-that-trigger-workflows#repository_dispatch)
event of type `documentdb-release` to
`documentdb/documentdb-kubernetes-operator`. That event is the **primary trigger**
for [`watch_documentdb_images.yml`](../../.github/workflows/watch_documentdb_images.yml),
which builds candidate `documentdb` + `gateway` images for the new version and
opens a "chore: bump DocumentDB images" PR (the human merge gate).

This replaces polling: instead of the operator repo checking upstream every day,
upstream notifies the operator repo the moment a release is published. The daily
cron in `watch_documentdb_images.yml` remains only as a safety net.

## Prerequisites

A credential in the **upstream** repo that is allowed to dispatch into the
operator repo. Either:

- A **fine-grained PAT** (or classic PAT with `repo` scope) belonging to a user
  with write access to `documentdb/documentdb-kubernetes-operator`, stored as a
  secret named `OPERATOR_DISPATCH_TOKEN`; **or**
- A **GitHub App** installed on the operator repo with `contents: write` (or the
  `repository_dispatch` permission), with its token minted at run time.

A fine-grained PAT scoped to only the operator repo with the **Contents:
read/write** permission is the least-privilege option.

## Reference workflow (add to `documentdb/documentdb`)

```yaml
# .github/workflows/notify-operator.yml  (in documentdb/documentdb)
name: Notify operator of new release

on:
  release:
    types: [published]

permissions:
  contents: read

jobs:
  dispatch:
    runs-on: ubuntu-latest
    steps:
      - name: Send repository_dispatch to operator repo
        env:
          # Least-privilege fine-grained PAT (Contents: write) scoped to the
          # operator repo, or a GitHub App token. Do NOT use the default
          # GITHUB_TOKEN — it cannot dispatch across repositories.
          DISPATCH_TOKEN: ${{ secrets.OPERATOR_DISPATCH_TOKEN }}
        run: |
          set -euo pipefail
          # Normalize tag (strip leading 'v'; dashed 0.110-0 -> dotted 0.110.0).
          RAW="${GITHUB_REF_NAME#v}"
          if [[ "$RAW" =~ ^[0-9]+\.[0-9]+-[0-9]+$ ]]; then
            VERSION="${RAW/-/.}"
          else
            VERSION="$RAW"
          fi
          echo "Dispatching documentdb-release for version $VERSION"
          # Omit apt_version: the operator derives the dashed Debian version
          # (0.111.0 -> 0.111-0) the APT repo expects. Only send apt_version
          # explicitly if the APT channel uses a non-standard revision.
          curl -fsSL -X POST \
            -H "Accept: application/vnd.github+json" \
            -H "Authorization: Bearer ${DISPATCH_TOKEN}" \
            -H "X-GitHub-Api-Version: 2022-11-28" \
            https://api.github.com/repos/documentdb/documentdb-kubernetes-operator/dispatches \
            -d "$(jq -nc --arg v "$VERSION" \
                  '{event_type:"documentdb-release", client_payload:{version:$v}}')"
```

## Payload contract

The operator's `watch_documentdb_images.yml` reads:

| Field                       | Required | Meaning                                                                 |
|-----------------------------|----------|-------------------------------------------------------------------------|
| `client_payload.version`    | yes      | Dotted semver of the release (e.g. `0.111.0`). Drives image tag + track. |
| `client_payload.apt_version`| no       | Debian package version of `postgresql-18-documentdb` to pin, in dashed Debian format (e.g. `0.111-0`). Defaults to the dashed form of `version`. |

The official APT `stable` channel serves **dashed** Debian versions (e.g.
`0.111-0`), not dotted semver. If omitted, the operator derives `apt_version`
by converting the dotted `version` to its dashed form. Set it explicitly only
if the APT channel publishes a revision that doesn't follow that convention.

## Testing without upstream

Until the sender lands upstream, the same flow can be exercised manually:

- Manually run `watch_documentdb_images.yml` via **workflow_dispatch** with a
  `version` (and optional `documentdb_apt_version`) input, or
- Send a one-off dispatch with a token that has access to the operator repo:

  ```bash
  curl -fsSL -X POST \
    -H "Authorization: Bearer <TOKEN>" \
    -H "Accept: application/vnd.github+json" \
    https://api.github.com/repos/documentdb/documentdb-kubernetes-operator/dispatches \
    -d '{"event_type":"documentdb-release","client_payload":{"version":"0.111.0"}}'
  ```
