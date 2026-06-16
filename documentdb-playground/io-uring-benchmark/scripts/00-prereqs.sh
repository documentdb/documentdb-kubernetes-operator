#!/usr/bin/env bash
# 00-prereqs.sh — verify tooling and that the cluster nodes can actually run io_uring.
#
# Checks:
#   * required CLIs present
#   * cluster reachable
#   * node kernel >= 5.1 and /proc/sys/kernel/io_uring_disabled == 0
#   * a fast (NVMe / premium SSD) StorageClass is available for headline numbers
source "$(cd "$(dirname "$0")" && pwd)/lib.sh"

require kubectl helm jq

log "kubectl context: $(kubectl config current-context)"
kubectl version -o json >/dev/null 2>&1 || die "cannot reach the cluster"

log "checking node kernel + io_uring sysctl (ephemeral pod)..."
OUT="$(kubectl run iouring-precheck --rm -i --restart=Never --image=busybox:1.36 --command -- \
  sh -c 'echo KERNEL=$(uname -r); echo IO_URING_DISABLED=$(cat /proc/sys/kernel/io_uring_disabled 2>/dev/null || echo missing)' 2>/dev/null || true)"
echo "$OUT" | grep -E 'KERNEL=|IO_URING_DISABLED=' || warn "could not read node kernel info"

KVER="$(echo "$OUT" | sed -n 's/^KERNEL=//p' | cut -d- -f1)"
DISABLED="$(echo "$OUT" | sed -n 's/^IO_URING_DISABLED=//p')"
if [ -n "$KVER" ]; then
  major="${KVER%%.*}"; rest="${KVER#*.}"; minor="${rest%%.*}"
  if [ "$major" -lt 5 ] || { [ "$major" -eq 5 ] && [ "$minor" -lt 1 ]; }; then
    warn "kernel $KVER may be too old for io_uring (need >= 5.1)"
  else
    log "kernel $KVER supports io_uring."
  fi
fi
case "$DISABLED" in
  0) log "io_uring is enabled on the node (io_uring_disabled=0)." ;;
  missing) warn "io_uring_disabled sysctl not found; assuming enabled (older kernel)." ;;
  *) warn "io_uring_disabled=$DISABLED — io_uring is DISABLED on this node. Re-enable it (sysctl kernel.io_uring_disabled=0) or io_uring will fail." ;;
esac

log "available StorageClasses:"
kubectl get storageclass
warn "For headline numbers, point spec.resource.storage.storageClass at a fast NVMe/premium-SSD class (e.g. AKS 'managed-csi-premium', EKS 'gp3'/io2)."

log "prereq checks complete."
