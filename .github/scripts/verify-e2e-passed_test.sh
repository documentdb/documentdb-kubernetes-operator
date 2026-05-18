#!/usr/bin/env bash
# Tests for .github/scripts/verify-e2e-passed.sh.
#
# We can't unit-test the inline bash that the workflow used to embed, but
# now that the logic lives in a script we exercise it with a mock `gh`
# binary on PATH. This catches the regressions that motivated the
# extraction:
#   - filtering against the workflow filename (test-e2e.yml) rather than
#     a brittle display name,
#   - the SHA resolution → exit 1 path (release would have promoted
#     blindly otherwise),
#   - FORCE=true must short-circuit before any gh call,
#   - non-success conclusions must fail the gate.
#
# Run with: bash .github/scripts/verify-e2e-passed_test.sh
# Required: bash 4+, mktemp, awk.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SUT="${SCRIPT_DIR}/verify-e2e-passed.sh"
if [[ ! -x "${SUT}" ]]; then
  echo "FATAL: ${SUT} not found or not executable" >&2
  exit 2
fi

PASS=0
FAIL=0
FAIL_NAMES=()

# Each test case spins up an isolated tmpdir holding a stub `gh` and a
# fake $GITHUB_STEP_SUMMARY. The stub's behavior is configured per case
# via a pair of env vars consumed by the stub itself.
make_tmp() {
  TMPDIR="$(mktemp -d)"
  STUB_LOG="${TMPDIR}/gh.log"
  STUB_BIN="${TMPDIR}/gh"
  SUMMARY="${TMPDIR}/summary.md"
  : > "${SUMMARY}"
  : > "${STUB_LOG}"
}

# Writes a `gh` stub that:
#   - records its argv to ${STUB_LOG} (one line per invocation),
#   - returns ${GH_STUB_SHA} when called for /commits/<ref>,
#   - returns whatever the test sets in ${GH_STUB_CONCLUSION} /
#     ${GH_STUB_URL} for /actions/workflows/test-e2e.yml/runs queries,
#   - exits non-zero when ${GH_STUB_FAIL_COMMITS}=1 to simulate an
#     unresolvable source_ref.
write_stub_gh() {
  cat > "${STUB_BIN}" <<'STUB'
#!/usr/bin/env bash
echo "$@" >> "${STUB_LOG:?}"
# Find the URL argument (the one that contains "repos/").
url=""
for a in "$@"; do
  case "$a" in
    repos/*) url="$a" ;;
  esac
done
case "$url" in
  *"/commits/"*)
    if [[ "${GH_STUB_FAIL_COMMITS:-0}" == "1" ]]; then
      exit 1
    fi
    printf '%s' "${GH_STUB_SHA:-}"
    ;;
  *"/actions/workflows/test-e2e.yml/runs"*)
    # Honour the --jq selector roughly: if it asks for .conclusion
    # return GH_STUB_CONCLUSION, else GH_STUB_URL.
    jq=""
    next=0
    for a in "$@"; do
      if [[ $next -eq 1 ]]; then jq="$a"; break; fi
      if [[ "$a" == "--jq" ]]; then next=1; fi
    done
    case "$jq" in
      *conclusion*) printf '%s' "${GH_STUB_CONCLUSION:-}" ;;
      *html_url*)   printf '%s' "${GH_STUB_URL:-}" ;;
      *)            printf '%s' "${GH_STUB_CONCLUSION:-}" ;;
    esac
    ;;
  *)
    echo "stub: unexpected url '$url'" >&2
    exit 99
    ;;
esac
STUB
  chmod +x "${STUB_BIN}"
}

# run_sut runs verify-e2e-passed.sh in an isolated env. Writes captured
# stdout to $OUT and stderr to $ERR; returns the script's exit code in
# $RC. Caller must have called make_tmp + write_stub_gh.
run_sut() {
  OUT="${TMPDIR}/stdout"
  ERR="${TMPDIR}/stderr"
  PATH="${TMPDIR}:${PATH}" \
  GH_TOKEN=test-token \
  REPO="documentdb/documentdb-kubernetes-operator" \
  SOURCE_REF="${1:?ref required}" \
  FORCE="${FORCE:-false}" \
  GITHUB_ACTOR="test-user" \
  GITHUB_STEP_SUMMARY="${SUMMARY}" \
  STUB_LOG="${STUB_LOG}" \
  GH_STUB_SHA="${GH_STUB_SHA:-}" \
  GH_STUB_CONCLUSION="${GH_STUB_CONCLUSION:-}" \
  GH_STUB_URL="${GH_STUB_URL:-}" \
  GH_STUB_FAIL_COMMITS="${GH_STUB_FAIL_COMMITS:-0}" \
    bash "${SUT}" >"${OUT}" 2>"${ERR}"
  RC=$?
}

assert_eq() {
  local name="$1" expected="$2" actual="$3"
  if [[ "${expected}" == "${actual}" ]]; then
    echo "  ✓ ${name}"
  else
    echo "  ✗ ${name}: expected '${expected}', got '${actual}'"
    return 1
  fi
}

assert_contains() {
  local name="$1" expected="$2" haystack="$3"
  if [[ "${haystack}" == *"${expected}"* ]]; then
    echo "  ✓ ${name}"
  else
    echo "  ✗ ${name}: expected to find '${expected}' in:"
    echo "${haystack}" | sed 's/^/      /'
    return 1
  fi
}

assert_not_contains() {
  local name="$1" forbidden="$2" haystack="$3"
  if [[ "${haystack}" != *"${forbidden}"* ]]; then
    echo "  ✓ ${name}"
  else
    echo "  ✗ ${name}: forbidden string '${forbidden}' present in:"
    echo "${haystack}" | sed 's/^/      /'
    return 1
  fi
}

run_case() {
  local name="$1"; shift
  echo "▶ ${name}"
  if "$@"; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    FAIL_NAMES+=("${name}")
  fi
}

# ----------------------------------------------------------------------
# 1. force=true short-circuits before any gh call.
# ----------------------------------------------------------------------
case_force_skips_gh() {
  make_tmp
  write_stub_gh
  FORCE=true \
  GH_STUB_SHA="" GH_STUB_CONCLUSION="" GH_STUB_URL="" \
    run_sut "main"
  local ok=0
  assert_eq "exits 0"          "0" "${RC}"           || ok=1
  assert_contains "summary mentions break-glass" "force=true" "$(cat "${SUMMARY}")" || ok=1
  assert_contains "summary records actor"        "test-user"  "$(cat "${SUMMARY}")" || ok=1
  assert_eq "did not call gh"  "0" "$(wc -l < "${STUB_LOG}" | tr -d ' ')" || ok=1
  return ${ok}
}

# ----------------------------------------------------------------------
# 2. successful E2E conclusion → exit 0 + checkmark in summary.
# ----------------------------------------------------------------------
case_success_passes() {
  make_tmp
  write_stub_gh
  GH_STUB_SHA="abc123def4567890abc123def4567890abc12345" \
  GH_STUB_CONCLUSION="success" \
  GH_STUB_URL="https://github.com/example/runs/42" \
    run_sut "main"
  local ok=0
  assert_eq "exits 0"           "0"  "${RC}"          || ok=1
  assert_contains "summary marks success" "verified on source_ref" "$(cat "${SUMMARY}")" || ok=1
  assert_contains "summary includes run URL" "https://github.com/example/runs/42" "$(cat "${SUMMARY}")" || ok=1
  # gh must have been called with the workflow-FILENAME path, not the
  # display name — that's the bug-fence from the rubber-duck critique.
  assert_contains "queried by filename" "actions/workflows/test-e2e.yml/runs" "$(cat "${STUB_LOG}")" || ok=1
  return ${ok}
}

# ----------------------------------------------------------------------
# 3. non-success conclusion → exit 1 + failure summary including conclusion.
# ----------------------------------------------------------------------
case_failure_blocks() {
  make_tmp
  write_stub_gh
  GH_STUB_SHA="deadbeef" \
  GH_STUB_CONCLUSION="failure" \
  GH_STUB_URL="https://github.com/example/runs/99" \
    run_sut "main"
  local ok=0
  assert_eq "exits 1"           "1" "${RC}"           || ok=1
  assert_contains "summary marks failure" "verification failed" "$(cat "${SUMMARY}")" || ok=1
  assert_contains "summary includes conclusion" "failure" "$(cat "${SUMMARY}")"     || ok=1
  assert_contains "stderr signals error" "::error::" "$(cat "${ERR}")"               || ok=1
  return ${ok}
}

# ----------------------------------------------------------------------
# 4. cancelled / timed_out / nothing-found all fail closed.
# ----------------------------------------------------------------------
case_cancelled_blocks() {
  make_tmp; write_stub_gh
  GH_STUB_SHA="cafe" GH_STUB_CONCLUSION="cancelled" GH_STUB_URL="" \
    run_sut "main"
  assert_eq "cancelled exits 1" "1" "${RC}"
}
case_no_run_blocks() {
  make_tmp; write_stub_gh
  GH_STUB_SHA="cafe" GH_STUB_CONCLUSION="" GH_STUB_URL="" \
    run_sut "main"
  local ok=0
  assert_eq "no-run exits 1"      "1" "${RC}" || ok=1
  assert_contains "summary 'none'" "\`none\`" "$(cat "${SUMMARY}")" || ok=1
  return ${ok}
}

# ----------------------------------------------------------------------
# 5. unresolvable source_ref → exit 1 with a clear error.
# ----------------------------------------------------------------------
case_sha_resolution_fails() {
  make_tmp
  write_stub_gh
  GH_STUB_FAIL_COMMITS=1 \
    run_sut "no-such-ref"
  local ok=0
  assert_eq "exits 1"             "1" "${RC}"           || ok=1
  assert_contains "stderr error"  "Could not resolve source_ref" "$(cat "${ERR}")" || ok=1
  # We must NOT proceed to query workflow runs once the SHA is missing.
  assert_not_contains "did not query runs" "actions/workflows" "$(cat "${STUB_LOG}")" || ok=1
  return ${ok}
}

# ----------------------------------------------------------------------
# 6. missing required env vars → fail fast (set -u + parameter expansion).
# ----------------------------------------------------------------------
case_missing_repo_env() {
  make_tmp
  write_stub_gh
  OUT="${TMPDIR}/stdout"
  ERR="${TMPDIR}/stderr"
  PATH="${TMPDIR}:${PATH}" \
  GH_TOKEN=test-token \
  SOURCE_REF=main \
  FORCE=false \
    bash "${SUT}" >"${OUT}" 2>"${ERR}"
  local rc=$?
  assert_eq "exits non-zero" "1" "${rc}"
}

# ----------------------------------------------------------------------
# Drive table.
# ----------------------------------------------------------------------
run_case "force=true skips all gh calls"               case_force_skips_gh
run_case "success conclusion exits 0"                  case_success_passes
run_case "failure conclusion exits 1"                  case_failure_blocks
run_case "cancelled conclusion exits 1"                case_cancelled_blocks
run_case "no completed run found exits 1"              case_no_run_blocks
run_case "SHA resolution failure exits 1"              case_sha_resolution_fails
run_case "missing REPO env exits non-zero"             case_missing_repo_env

echo
echo "===================="
echo "Passed: ${PASS}"
echo "Failed: ${FAIL}"
if (( FAIL > 0 )); then
  printf 'FAILED: %s\n' "${FAIL_NAMES[@]}"
  exit 1
fi
exit 0
