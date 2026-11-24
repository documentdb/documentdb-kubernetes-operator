#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
#
# Fails if a local image ships an SSH client or server, a shell, or a package
# manager, so a distro base and its OpenSSH (SSHole, CVE-2026-123158) can't
# return to the operator image unnoticed. Matches file names only.
#
# Usage: assert-minimal-image.sh <image>
set -euo pipefail

IMAGE="${1:?usage: $0 <image>}"

# The dummy command lets images without ENTRYPOINT or CMD be created; nothing runs.
container=$(docker create "$IMAGE" unused)
trap 'docker rm -f "$container" >/dev/null' EXIT

# Listed separately so a failed export fails the check instead of passing it.
files=$(docker export "$container" | tar -tf -)
[[ -n "$files" ]] || { echo "::error::$IMAGE exported no files" >&2; exit 1; }

# Directories end in "/", so only files and links can match; "(^|/)" also covers "./" prefixes.
rc=0
found=$(grep -E \
  '(^|/)(ssh[-.a-z0-9]*|scp|sftp|sftp-server|dropbear[a-z]*|sh|ash|bash|rbash|dash|zsh|ksh|mksh|csh|tcsh|fish|busybox|toybox|apk|apk\.static|apt|apt-get|dpkg|rpm|dnf|tdnf|yum|microdnf|zypper|pacman|opkg)$' \
  <<<"$files") || rc=$?
# grep exits 1 for no match; anything above that is an error, not a pass.
(( rc <= 1 )) || { echo "::error::grep failed with exit $rc" >&2; exit "$rc"; }

if [[ -n "$found" ]]; then
  echo "::error::$IMAGE ships files a distroless image must not have:" >&2
  echo "$found" >&2
  exit 1
fi
echo "OK: $IMAGE has no SSH, shell or package manager"
