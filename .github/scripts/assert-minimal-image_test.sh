#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
#
# Negative tests for assert-minimal-image.sh, using throwaway images built
# with `docker import` so no registry access is needed.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORK=$(mktemp -d)
REPO=assert-minimal-image-test
cleanup() {
  rm -rf "$WORK"
  docker images -q "$REPO" | sort -u | xargs -r docker rmi -f >/dev/null
}
trap cleanup EXIT

# make_image <name> <path>... : image with an empty file at each path
make_image() {
  local name="$REPO:$1" root="$WORK/$1"; shift
  mkdir -p "$root/usr/sbin" "$root/tmp"
  for p in "$@"; do mkdir -p "$root/$(dirname "$p")"; : > "$root/$p"; done
  tar -C "$root" -c . | docker import --change 'ENTRYPOINT ["/manager"]' - "$name" >/dev/null
}

expect() {
  local want="$1" image="$REPO:$2"
  if bash "$SCRIPT_DIR/assert-minimal-image.sh" "$image" >/dev/null 2>&1; then got=pass; else got=fail; fi
  if [[ "$got" != "$want" ]]; then
    echo "FAIL: $image expected $want, got $got" >&2
    exit 1
  fi
  echo "PASS: $image -> $want"
}

# Built here, not in $(...), so a failed import stops the test.
make_image good manager etc/ssl/certs/ca.pem usr/lib/rpm/macros
make_image sshd manager usr/sbin/sshd
make_image sshd-session manager usr/lib/ssh/sshd-session
make_image ssh-client manager usr/bin/ssh
make_image dropbear manager usr/sbin/dropbear
make_image shell manager bin/sh
make_image busybox manager bin/busybox
make_image apk manager sbin/apk
make_image tdnf manager usr/bin/tdnf

expect pass good
for name in sshd sshd-session ssh-client dropbear shell busybox apk tdnf; do
  expect fail "$name"
done
