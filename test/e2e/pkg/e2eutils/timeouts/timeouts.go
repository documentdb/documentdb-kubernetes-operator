// Package timeouts centralises the Eventually/Consistently durations
// used by the DocumentDB E2E suite. Where an operation corresponds to a
// CNPG event already modelled by
// github.com/cloudnative-pg/cloudnative-pg/tests/utils/timeouts, this
// package reuses the CNPG value (converted to a time.Duration); for
// DocumentDB-specific operations it defines opinionated defaults.
package timeouts

import (
	"time"

	cnpgtimeouts "github.com/cloudnative-pg/cloudnative-pg/tests/utils/timeouts"
)

// Op is a DocumentDB-specific operation identifier. New callers should
// prefer the constants below over ad-hoc strings so that the helper can
// surface un-mapped operations via the UnknownOpFallback.
type Op string

// DocumentDB-specific operations. When adding an entry here, also
// extend documentDBDefaults/cnpgAlias (and PollInterval if the new op
// needs a non-default poll cadence).
const (
	// DocumentDBReady waits for a fresh DocumentDB cluster to reach the
	// running state after creation.
	DocumentDBReady Op = "documentDBReady"
	// DocumentDBUpgrade waits for an in-place image upgrade rollout.
	DocumentDBUpgrade Op = "documentDBUpgrade"
	// InstanceScale waits for a replica count change to converge.
	InstanceScale Op = "instanceScale"
	// PVCResize waits for a StorageConfiguration.PvcSize change to be
	// applied across all PVCs.
	PVCResize Op = "pvcResize"
	// BackupComplete waits for a Backup CR to reach Completed.
	BackupComplete Op = "backupComplete"
	// RestoreComplete waits for a recovery bootstrap to complete.
	RestoreComplete Op = "restoreComplete"
	// MongoConnect bounds a single mongo client connect/ping attempt.
	MongoConnect Op = "mongoConnect"
	// ServiceReady waits for a LoadBalancer / ClusterIP to acquire an
	// address and begin routing.
	ServiceReady Op = "serviceReady"
)

// UnknownOpFallback is returned by For when an Op is not in the
// DocumentDB map and has no corresponding CNPG mapping.
const UnknownOpFallback = 2 * time.Minute

// documentDBDefaults captures the DocumentDB-specific defaults used by
// For. Keep this map in sync with the constants above.
var documentDBDefaults = map[Op]time.Duration{
	DocumentDBReady:   5 * time.Minute,
	DocumentDBUpgrade: 10 * time.Minute,
	InstanceScale:     5 * time.Minute,
	PVCResize:         5 * time.Minute,
	BackupComplete:    10 * time.Minute,
	RestoreComplete:   15 * time.Minute,
	MongoConnect:      30 * time.Second,
	ServiceReady:      2 * time.Minute,
}

// cnpgAlias maps selected DocumentDB ops to their CNPG counterparts.
// When the CNPG timeouts map (optionally overridden via the
// TEST_TIMEOUTS environment variable) contains the aliased event, its
// value — converted from seconds to time.Duration — wins over the
// DocumentDB default. This lets operators share a single tuning knob
// for cluster-readiness style waits.
var cnpgAlias = map[Op]cnpgtimeouts.Timeout{
	DocumentDBReady: cnpgtimeouts.ClusterIsReady,
	InstanceScale:   cnpgtimeouts.ClusterIsReady,
	BackupComplete:  cnpgtimeouts.BackupIsReady,
}

// For returns the Eventually timeout for op. Lookup order:
//  1. CNPG alias (honours TEST_TIMEOUTS env var if set).
//  2. DocumentDB default.
//  3. UnknownOpFallback for unknown ops.
func For(op Op) time.Duration {
	if alias, ok := cnpgAlias[op]; ok {
		if m, err := cnpgtimeouts.Timeouts(); err == nil {
			if s, ok := m[alias]; ok {
				return time.Duration(s) * time.Second
			}
		}
	}
	if d, ok := documentDBDefaults[op]; ok {
		return d
	}
	return UnknownOpFallback
}

// PollInterval returns the Eventually poll interval for op. Fast ops
// use a short 2-second poll; slow, cluster-level operations use a
// 10-second poll to reduce API-server churn during long waits.
func PollInterval(op Op) time.Duration {
	switch op {
	case MongoConnect, ServiceReady:
		return 2 * time.Second
	case DocumentDBReady, DocumentDBUpgrade, InstanceScale,
		PVCResize, BackupComplete, RestoreComplete:
		return 10 * time.Second
	default:
		return 5 * time.Second
	}
}

// AllOps returns the set of DocumentDB operations known to this
// package, in insertion order. Useful for table tests.
func AllOps() []Op {
	return []Op{
		DocumentDBReady,
		DocumentDBUpgrade,
		InstanceScale,
		PVCResize,
		BackupComplete,
		RestoreComplete,
		MongoConnect,
		ServiceReady,
	}
}
