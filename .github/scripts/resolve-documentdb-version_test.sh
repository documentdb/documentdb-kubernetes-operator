#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
export GITHUB_SHA=abcdef0123456789
export GITHUB_RUN_ID=123
export GITHUB_RUN_ATTEMPT=2
export DOCUMENTDB_GATEWAY_IMAGE_REPO=ghcr.io/documentdb/documentdb/documentdb-local

assert_output() {
  local expected="$1"
  # Whole-line match, so a renamed key (e.g. wrong_source_release_tag) fails.
  if [[ $'\n'"$OUTPUT"$'\n' != *$'\n'"$expected"$'\n'* ]]; then
    printf 'Missing expected output: %s\nActual output:\n%s\n' "$expected" "$OUTPUT" >&2
    exit 1
  fi
}

check_version() {
  local raw_version="$1" version="$2" schema="$3" source="$4" package="$5" tag="$6"
  OUTPUT=$(RAW_VERSION="$raw_version" bash "$SCRIPT_DIR/resolve-documentdb-version.sh")
  assert_output "documentdb_version=$version"
  assert_output "documentdb_version_dash=$schema"
  assert_output "expected_deb_version=$package"
  assert_output "package_source=$source"
  assert_output "source_release_tag=$tag"
  assert_output "image_tag=${version}-build-123-2-abcdef0"
  assert_output "gateway_source_image=${DOCUMENTDB_GATEWAY_IMAGE_REPO}:pg17-${version}"
  printf 'PASS: %s\n' "$raw_version"
}

check_version 0.113.0 0.113.0 0.113-0 apt 0.113-0 ""
check_version 0.113-0 0.113.0 0.113-0 apt 0.113-0 ""
check_version 1.0.0 1.0.0 1.0-0 apt 1.0-0 ""
check_version 1.0-RC2 1.0-rc2 1.0-0 source '1.0~rc2' v1.0-RC2
check_version v1.0-RC2 1.0-rc2 1.0-0 source '1.0~rc2' v1.0-RC2
check_version 1.0-rc2 1.0-rc2 1.0-0 source '1.0~rc2' v1.0-RC2
check_version 1.0.0-rc2 1.0-rc2 1.0-0 source '1.0~rc2' v1.0-RC2
check_version '1.0~rc2' 1.0-rc2 1.0-0 source '1.0~rc2' v1.0-RC2
check_version 1.0-RC12 1.0-rc12 1.0-0 source '1.0~rc12' v1.0-RC12
check_version 1.0.1-RC2 1.0.1-rc2 1.0-1 source '1.0.1~rc2' v1.0.1-RC2

OUTPUT=$(DOCUMENTDB_PACKAGE_SOURCE=source RAW_VERSION=1.0.0 bash "$SCRIPT_DIR/resolve-documentdb-version.sh")
assert_output "documentdb_version=1.0.0"
assert_output "documentdb_version_dash=1.0-0"
assert_output "expected_deb_version=1.0-0"
assert_output "package_source=source"
assert_output "source_release_tag=v1.0-0"
assert_output "gateway_source_image=${DOCUMENTDB_GATEWAY_IMAGE_REPO}:pg17-1.0.0"
printf 'PASS: 1.0.0 forced to source\n'

if DOCUMENTDB_PACKAGE_SOURCE=pgdg RAW_VERSION=1.0.0 bash "$SCRIPT_DIR/resolve-documentdb-version.sh" >/dev/null 2>&1; then
  printf 'FAIL: invalid package source accepted\n' >&2
  exit 1
fi
printf 'PASS: rejected package source pgdg\n'

for case in 'apt 1.0-RC2' 'pgdg 1.0-RC2'; do
  read -r source version <<<"$case"
  if DOCUMENTDB_PACKAGE_SOURCE="$source" RAW_VERSION="$version" bash "$SCRIPT_DIR/resolve-documentdb-version.sh" >/dev/null 2>&1; then
    printf 'FAIL: package source %s accepted for %s\n' "$source" "$version" >&2
    exit 1
  fi
  printf 'PASS: rejected package source %s for %s\n' "$source" "$version"
done
OUTPUT=$(DOCUMENTDB_PACKAGE_SOURCE=source RAW_VERSION=1.0-RC2 bash "$SCRIPT_DIR/resolve-documentdb-version.sh")
assert_output "source_release_tag=v1.0-RC2"
printf 'PASS: 1.0-RC2 with package source source\n'

for case in 'apt 1.0.0 apt' 'auto 1.0.0 apt' 'auto 1.0-RC2 source'; do
  read -r requested version expected <<<"$case"
  OUTPUT=$(DOCUMENTDB_PACKAGE_SOURCE="$requested" RAW_VERSION="$version" bash "$SCRIPT_DIR/resolve-documentdb-version.sh")
  assert_output "package_source=$expected"
  printf 'PASS: %s with package source %s\n' "$version" "$requested"
done

for invalid in '' 1.0 latest 1.0-RC0 1.0-RC02 1.0-RC 1.0-RC2-extra '1.0-RC2$(id)'; do
  if RAW_VERSION="$invalid" bash "$SCRIPT_DIR/resolve-documentdb-version.sh" >/dev/null 2>&1; then
    printf 'FAIL: invalid version accepted: %s\n' "$invalid" >&2
    exit 1
  fi
  printf 'PASS: rejected %s\n' "${invalid:-empty version}"
done