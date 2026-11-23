#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.
#
# Fails if a local image ships an SSH server or client, a shell, or a package
# manager. Keeps the operator image distroless so distro OpenSSH CVEs
# (e.g. SSHole, CVE-2026-123158) cannot reach it again.
#
# Usage: assert-minimal-image.sh <image>
set -euo pipefail

IMAGE="${1:?usage: $0 <image>}"

container=$(docker create "$IMAGE")
trap 'docker rm -f "$container" >/dev/null' EXIT

# Listed separately so a failed export fails the check instead of passing it.
files=$(docker export "$container" | tar -tf -)
[[ -n "$files" ]] || { echo "::error::$IMAGE exported no files" >&2; exit 1; }

# Directories end in "/", so only files and links can match.
found=$(sed 's|^\./||' <<<"$files" | grep -E \
  '(^|/)(sshd?|sshd-session|sshd-auth|ssh-keygen|ssh-agent|scp|sftp|sftp-server|dropbear[a-z]*|sh|ash|bash|dash|zsh|busybox|apk|apt|apt-get|dpkg|rpm|dnf|tdnf|yum|microdnf)$' \
  || true)

if [[ -n "$found" ]]; then
  echo "::error::$IMAGE ships files a distroless image must not have:" >&2
  echo "$found" >&2
  exit 1
fi
echo "OK: $IMAGE has no SSH, shell or package manager"
