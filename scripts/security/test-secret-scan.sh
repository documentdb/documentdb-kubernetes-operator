#!/usr/bin/env bash
#
# Verifies that the gitleaks configuration in .gitleaks.toml actually detects
# the secret types we care about (and ignores known placeholders).
#
# It works by generating throwaway fixture files containing FAKE secrets in a
# temp directory, running gitleaks against them, and asserting the expected
# rules fire. No real or fixture secrets are ever committed to the repo.
#
# Usage:
#   scripts/security/test-secret-scan.sh                # uses gitleaks on PATH
#   GITLEAKS_BIN=/tmp/gitleaks scripts/security/test-secret-scan.sh
#
# Exit code 0 = all assertions passed.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${REPO_ROOT}/.gitleaks.toml"
GITLEAKS_BIN="${GITLEAKS_BIN:-gitleaks}"

if ! command -v "${GITLEAKS_BIN}" >/dev/null 2>&1; then
  echo "ERROR: gitleaks not found (set GITLEAKS_BIN or install gitleaks)." >&2
  exit 2
fi
if [ ! -f "${CONFIG}" ]; then
  echo "ERROR: config not found at ${CONFIG}" >&2
  exit 2
fi

WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

# ---- Fixtures that MUST be flagged (fake but pattern-valid) -----------------
cat > "${WORK}/leaks.txt" <<'EOF'
password = "sup3rSecretValue"
mongodb://admin:hunter2@db.internal:27017/app
-----BEGIN RSA PRIVATE KEY-----
MIIEvfakekeymaterialdoesnotmatterforregexmatching
-----END RSA PRIVATE KEY-----
InstrumentationKey=11111111-2222-3333-4444-555555555555
APPLICATIONINSIGHTS_CONNECTION_STRING=InstrumentationKey=11111111-2222-3333-4444-555555555555;IngestionEndpoint=https://eastus.in.applicationinsights.azure.com/
subscriptionId: 99999999-8888-7777-6666-555555555555
/subscriptions/12345678-90ab-cdef-1234-567890abcdef/resourceGroups/rg
mongodb://docdbadmin:R3alSecret!@10.0.0.5:10260/?tls=true
mongodb+srv://docdbadmin:R3alSecret!@my-cluster.mongocluster.cosmos.azure.com/
EOF

# ---- Fixtures that must NOT be flagged (placeholders) -----------------------
cat > "${WORK}/placeholders.txt" <<'EOF'
password = "changeme"
password: ${DB_PASSWORD}
InstrumentationKey=00000000-0000-0000-0000-000000000000
mongodb://username:password@<EXTERNAL-IP>:10260/
mongodb+srv://user:pass@my-cluster.mongocluster.cosmos.azure.com/
EOF

REPORT="${WORK}/report.json"
set +e
"${GITLEAKS_BIN}" detect \
  --source "${WORK}" \
  --config "${CONFIG}" \
  --no-git \
  --report-format json \
  --report-path "${REPORT}" \
  --redact >/dev/null 2>&1
set -e

if [ ! -s "${REPORT}" ]; then
  echo "FAIL: gitleaks produced no findings; expected several." >&2
  exit 1
fi

fail=0

assert_rule_present() {
  local rule="$1"
  if grep -q "\"RuleID\": *\"${rule}\"" "${REPORT}"; then
    echo "PASS  detected: ${rule}"
  else
    echo "FAIL  missing:  ${rule}" >&2
    fail=1
  fi
}

assert_no_finding_in() {
  # Assert no finding references the placeholder file.
  local file="$1"
  if grep -q "placeholders.txt" "${REPORT}"; then
    echo "FAIL  placeholder in ${file} was flagged (false positive)" >&2
    fail=1
  else
    echo "PASS  placeholders ignored"
  fi
}

assert_rule_present "generic-password-assignment"
assert_rule_present "private-key-block"
assert_rule_present "connection-string-with-credentials"
assert_rule_present "azure-appinsights-instrumentation-key"
assert_rule_present "azure-appinsights-connection-string"
assert_rule_present "azure-subscription-id"
assert_rule_present "documentdb-connection-string"
assert_no_finding_in "placeholders.txt"

if [ "${fail}" -ne 0 ]; then
  echo "" >&2
  echo "Secret-scan verification FAILED. Review .gitleaks.toml rules." >&2
  exit 1
fi

echo ""
echo "All secret-scan assertions passed."
