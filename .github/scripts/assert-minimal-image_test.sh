#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
#
# Negative tests for assert-minimal-image.sh, using throwaway images built
# with `docker import` so no registry access is needed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORK=$(mktemp -d)
# Unique per run so parallel runs on one daemon don't delete each other's images.
REPO="assert-minimal-image-test-$$"
cleanup() {
  rm -rf "$WORK"
  docker images -q "$REPO" | sort -u | xargs -r docker rmi -f >/dev/null
}
trap cleanup EXIT

# make_image <name> <path>... : image with an empty file at each path. No
# ENTRYPOINT or CMD, so every case also covers images that have neither.
make_image() {
  local name="$REPO:$1" root="$WORK/$1"; shift
  mkdir -p "$root/usr/sbin" "$root/tmp"
  for p in "$@"; do mkdir -p "$root/$(dirname "$p")"; : > "$root/$p"; done
  tar -C "$root" -c . | docker import - "$name" >/dev/null
}

# check <name> [path] : a clean image must pass; with the path added, the
# check must fail and name that path, so an unrelated error can't count.
check() {
  local name="$1" path="${2:-}" out
  if [[ -z "$path" ]]; then
    make_image "$name" manager etc/ssl/certs/ca.pem usr/lib/rpm/macros var/cache/tdnf/x
    bash "$SCRIPT_DIR/assert-minimal-image.sh" "$REPO:$name" >/dev/null
    echo "PASS: clean image passes"
    return
  fi
  make_image "$name" manager "$path"
  if out=$(bash "$SCRIPT_DIR/assert-minimal-image.sh" "$REPO:$name" 2>&1); then
    echo "FAIL: $path was not reported" >&2
    exit 1
  fi
  # docker export may or may not prefix paths with "./".
  if [[ $'\n'"$out"$'\n' != *$'\n'"$path"$'\n'* && $'\n'"$out"$'\n' != *$'\n'"./$path"$'\n'* ]]; then
    printf 'FAIL: %s failed for another reason:\n%s\n' "$path" "$out" >&2
    exit 1
  fi
  echo "PASS: $path reported"
}

check clean
check sshd usr/sbin/sshd
check sshd-session usr/lib/ssh/sshd-session
check sshd-pam usr/sbin/sshd.pam
check ssh-keysign usr/lib/ssh/ssh-keysign
check ssh-client usr/bin/ssh
check dropbear usr/sbin/dropbear
check shell bin/sh
check mksh bin/mksh
check busybox bin/busybox
check toybox bin/toybox
check apk sbin/apk
check apk-static sbin/apk.static
check tdnf usr/bin/tdnf
check pacman usr/bin/pacman
