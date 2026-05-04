#!/usr/bin/env bash
# Verifies that TEST - E2E (.github/workflows/test-e2e.yml) ran to a
# `success` conclusion on the commit identified by SOURCE_REF before
# allowing a release workflow to promote candidate images.
#
# Contract (read carefully — this matters for release auditability):
#   - This gate ensures the Helm-chart packaging SHA has E2E coverage.
#   - It does NOT prove the candidate image (passed via candidate_version
#     to the calling workflow) was built from SOURCE_REF. Image-to-source
#     attestation would require signed provenance and is out of scope.
#     Operators MUST ensure source_ref matches the commit the candidate
#     was built from before invoking the release workflow.
#
# Inputs (env):
#   GH_TOKEN        - PAT/installation token with `actions:read`,
#                     `checks:read`, `contents:read` on the repo.
#   REPO            - "owner/name" (typically ${{ github.repository }}).
#   SOURCE_REF      - Branch, tag, or commit SHA to verify.
#   FORCE           - "true" → bypass verification (break-glass).
#   GITHUB_ACTOR    - For step-summary attribution when FORCE=true.
#                     Optional outside Actions.
#   GITHUB_STEP_SUMMARY - Path to append summary lines. Optional outside
#                     Actions; defaults to /dev/null when unset.
#
# Exit codes:
#   0 - verification passed (or skipped via FORCE=true).
#   1 - verification failed: missing inputs, SHA resolution failure, or
#       no successful TEST - E2E run found for the resolved SHA.

set -euo pipefail

: "${REPO:?REPO must be set (e.g., owner/name)}"
: "${SOURCE_REF:?SOURCE_REF must be set}"
: "${GH_TOKEN:?GH_TOKEN must be set}"

FORCE="${FORCE:-false}"
ACTOR="${GITHUB_ACTOR:-unknown}"
SUMMARY="${GITHUB_STEP_SUMMARY:-/dev/null}"

if [[ "${FORCE}" == "true" ]]; then
  {
    echo "## ⚠️ TEST - E2E verification skipped (force=true)"
    echo ""
    echo "- **Actor**: \`${ACTOR}\`"
    echo "- **source_ref**: \`${SOURCE_REF}\`"
    echo "- **Reason**: workflow_dispatch input \`force\` was set to true"
  } >> "${SUMMARY}"
  exit 0
fi

SHA="$(gh api "repos/${REPO}/commits/${SOURCE_REF}" --jq '.sha' 2>/dev/null || true)"
if [[ -z "${SHA}" ]]; then
  echo "::error::Could not resolve source_ref '${SOURCE_REF}' to a SHA" >&2
  exit 1
fi
echo "Resolved source_ref '${SOURCE_REF}' to ${SHA}"

# Pick the most recent completed test-e2e.yml run for this SHA. We filter by
# the workflow's filename (a stable identifier) rather than its display name
# because matrix-job display names are brittle and can drift.
RUNS_QUERY="repos/${REPO}/actions/workflows/test-e2e.yml/runs?head_sha=${SHA}&status=completed&per_page=1"
CONCLUSION="$(gh api "${RUNS_QUERY}" --jq '.workflow_runs[0].conclusion // ""' 2>/dev/null || true)"
RUN_URL="$(gh api "${RUNS_QUERY}" --jq '.workflow_runs[0].html_url // ""' 2>/dev/null || true)"

if [[ "${CONCLUSION}" != "success" ]]; then
  echo "::error::No successful TEST - E2E run found for ${SHA} (conclusion='${CONCLUSION:-none}')." >&2
  {
    echo "## ❌ TEST - E2E verification failed"
    echo ""
    echo "- **source_ref**: \`${SOURCE_REF}\` → \`${SHA}\`"
    echo "- **Latest TEST - E2E conclusion**: \`${CONCLUSION:-none}\`"
    if [[ -n "${RUN_URL}" ]]; then
      echo "- **Run URL**: ${RUN_URL}"
    fi
    echo ""
    echo "Re-run TEST - E2E for that commit, or set \`force=true\` (break-glass) to bypass."
  } >> "${SUMMARY}"
  exit 1
fi

{
  echo "## ✅ TEST - E2E verified on source_ref"
  echo ""
  echo "- **source_ref**: \`${SOURCE_REF}\` → \`${SHA}\`"
  echo "- **Run URL**: ${RUN_URL}"
} >> "${SUMMARY}"
