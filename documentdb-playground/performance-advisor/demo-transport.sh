#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT License.

set -euo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
[[ $# -ge 3 ]] || { printf 'Missing lifecycle transport arguments.\n' >&2; exit 1; }
container=$1
workspace=$2
shift 2
case "$1" in up|down|_workload) ;; *) printf 'Invalid lifecycle transport action.\n' >&2; exit 1 ;; esac
session=$(mktemp -d "$script_dir/.demo-transport.XXXXXXXX")
remote_session="$workspace/documentdb-playground/performance-advisor/${session##*/}"
transport_pid=
interrupted=false

cancel_remote() {
  touch "$session/cancel"
  for ((attempt=0; attempt<50; attempt++)); do
    [[ ! -s "$session/pid" ]] || break
    kill -0 "$transport_pid" 2>/dev/null || return 0
    sleep 0.1
  done
  [[ -s "$session/pid" ]] || {
    printf 'Remote lifecycle has not acknowledged cancellation; retaining %s.\n' "$session" >&2
    return 1
  }
  read -r pid started <"$session/pid"
  [[ "$pid" =~ ^[1-9][0-9]*$ && "$started" =~ ^[0-9]+$ ]] || return 1
  docker exec --user vscode "$container" bash -c '
    set -euo pipefail
    pid=$1
    started=$2
    [[ -r "/proc/$pid/stat" ]] || exit 0
    stat=$(<"/proc/$pid/stat")
    read -r -a fields <<<"${stat##*) }"
    [[ "${fields[19]}" == "$started" && "${fields[2]}" == "$pid" && "${fields[3]}" == "$pid" ]] ||
      { printf "Lifecycle process identity changed; refusing to signal it.\n" >&2; exit 1; }
    kill -TERM -- "-$pid"
    for ((attempt=0; attempt<100; attempt++)); do
      kill -0 -- "-$pid" 2>/dev/null || exit 0
      sleep 0.1
    done
    kill -KILL -- "-$pid"
  ' bash "$pid" "$started"
}

cleanup() {
  local rc=$?
  trap - EXIT INT TERM
  if [[ "$interrupted" == true ]]; then
    if ! cancel_remote; then exit 1; fi
  fi
  if [[ -n "$transport_pid" ]]; then
    if [[ "$interrupted" == true ]] && kill -0 "$transport_pid" 2>/dev/null; then
      kill -TERM "$transport_pid"
    fi
    wait "$transport_pid" || :
  fi
  rm -f -- "$session/pid" "$session/cancel"
  rmdir -- "$session"
  exit "$rc"
}
trap cleanup EXIT
trap 'interrupted=true; exit 130' INT
trap 'interrupted=true; exit 143' TERM

docker exec -i --user vscode --workdir "$workspace" "$container" \
  setsid --wait bash -c '
    set -euo pipefail
    session=$1
    shift
    stat=$(<"/proc/$$/stat")
    read -r -a fields <<<"${stat##*) }"
    printf "%s %s\n" "$$" "${fields[19]}" >"$session/pid"
    [[ -d "$session" && ! -e "$session/cancel" ]] || exit 130
    exec bash "$@"
  ' bash "$remote_session" "$workspace/documentdb-playground/performance-advisor/demo.sh" "$@" <&0 &
transport_pid=$!
wait "$transport_pid"
