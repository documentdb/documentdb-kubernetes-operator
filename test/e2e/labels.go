// Package e2e contains the DocumentDB Kubernetes Operator end-to-end test
// suite. See docs/designs/e2e-test-suite.md for the full design.
package e2e

import "github.com/onsi/ginkgo/v2"

// Ginkgo label constants used to select subsets of the DocumentDB E2E test
// suite at invocation time. Each area suite in tests/<area>/ applies its
// matching area label to every spec it runs; cross-cutting labels
// (Smoke/Basic/Destructive/Disruptive/Slow and the NeedsXxx capability
// labels) are applied by individual specs.
//
// Keep these in sync with the design document.
const (
	// Area labels — one per test area (tests/<area>/).
	LifecycleLabel   = "lifecycle"
	ScaleLabel       = "scale"
	DataLabel        = "data"
	PerformanceLabel = "performance"
	BackupLabel      = "backup"
	RecoveryLabel    = "recovery"
	TLSLabel         = "tls"
	FeatureLabel     = "feature-gates"
	ExposureLabel    = "exposure"
	StatusLabel      = "status"
	UpgradeLabel     = "upgrade"

	// Cross-cutting selectors.
	SmokeLabel       = "smoke"
	BasicLabel       = "basic"
	DestructiveLabel = "destructive"
	DisruptiveLabel  = "disruptive"
	SlowLabel        = "slow"

	// Capability labels — environments that don't provide a prerequisite
	// can filter these specs out.
	NeedsCertManagerLabel  = "needs-cert-manager"
	NeedsMetalLBLabel      = "needs-metallb"
	NeedsCSISnapshotsLabel = "needs-csi-snapshots"
	// NeedsCSIResizeLabel marks specs that require the cluster's
	// StorageClass to support online PVC expansion (allowVolumeExpansion=true
	// plus a resize-capable CSI driver). Environments that lack this
	// capability should filter with `--label-filter='!needs-csi-resize'`.
	NeedsCSIResizeLabel = "needs-csi-resize"
)

// Level labels expose the depth tier of a spec to Ginkgo's label filter.
// Phase 2 specs should attach exactly one of these alongside the area
// label so invocations can select, e.g., all "level:low" specs with
// `--label-filter=level:low`. These labels are informational — the
// authoritative gate remains [SkipUnlessLevel], which reads TEST_DEPTH
// at runtime.
var (
	LowLevelLabel     = ginkgo.Label("level:low")
	MediumLevelLabel  = ginkgo.Label("level:medium")
	HighLevelLabel    = ginkgo.Label("level:high")
	HighestLevelLabel = ginkgo.Label("level:highest")
	LowestLevelLabel  = ginkgo.Label("level:lowest")
)
