// Package namespaces produces deterministic per-spec Kubernetes
// namespace names for DocumentDB e2e tests. The canonical entry point
// is [NamespaceForSpec], which a spec calls from inside a BeforeEach to
// obtain a name unique to the current spec, parallel process, and run.
//
// The returned names are DNS-1123-compliant (lowercase, ≤63 chars) and
// stable: calling NamespaceForSpec twice from within the same spec
// produces the same name, which is what retry / recovery logic needs.
package namespaces

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"

	"github.com/onsi/ginkgo/v2"
)

// maxNameLen bounds the returned namespace name; Kubernetes rejects
// names longer than 63 characters for DNS-1123 labels.
const maxNameLen = 63

// runIDFunc is a package-level indirection so unit tests can inject a
// deterministic run id without plumbing the root e2e package (which
// would introduce an import cycle).
var runIDFunc = defaultRunID

// SetRunIDFunc overrides the run-id accessor. The root suite wires it
// during SetupSuite so NamespaceForSpec returns names that match the
// fixtures/teardown label selectors. Tests call this to inject a
// deterministic id.
func SetRunIDFunc(f func() string) {
	if f != nil {
		runIDFunc = f
	}
}

func defaultRunID() string {
	if v := os.Getenv("E2E_RUN_ID"); v != "" {
		return sanitizeSegment(v)
	}
	return "unset"
}

// NamespaceForSpec returns a deterministic namespace name for the
// currently-running Ginkgo spec. The name embeds the sanitized area
// label, the run id, the parallel process number, and an 8-character
// SHA-256 prefix derived from the spec's FullText. Collisions across
// specs are avoided by the hash; determinism within a spec is provided
// by the hash being a pure function of the FullText.
//
// If area is empty, "spec" is used. Callers should pass the area
// label constant (e.g., e2e.LifecycleLabel) to make failures easier to
// triage from kubectl output.
func NamespaceForSpec(area string) string {
	return buildName(area, ginkgo.CurrentSpecReport().FullText(), procID())
}

// procID returns the ginkgo parallel process id, defaulting to "1"
// when unset. Duplicated here (instead of shared with fixtures) to
// avoid a dependency cycle with the fixtures package.
func procID() string {
	if v := os.Getenv("GINKGO_PARALLEL_PROCESS"); v != "" {
		return v
	}
	return "1"
}

// buildName is the pure core of NamespaceForSpec, factored out to make
// it trivially unit-testable without a Ginkgo runtime.
func buildName(area, specText, proc string) string {
	areaPart := sanitizeSegment(area)
	if areaPart == "" {
		areaPart = "spec"
	}
	sum := sha256.Sum256([]byte(specText))
	hash := hex.EncodeToString(sum[:])[:8]
	runID := sanitizeSegment(runIDFunc())
	if runID == "" {
		runID = "unset"
	}
	name := fmt.Sprintf("e2e-%s-%s-p%s-%s", areaPart, runID, proc, hash)
	if len(name) <= maxNameLen {
		return name
	}
	// Truncate areaPart first, then runID, preserving the trailing
	// hash (which is what guarantees uniqueness).
	suffix := fmt.Sprintf("-p%s-%s", proc, hash)
	budget := maxNameLen - len("e2e-") - len(suffix) - 1 // -1 for the dash between area and runID
	if budget < 2 {
		// Degenerate input; fall back to hash-only.
		return ("e2e-" + hash + suffix)[:maxNameLen]
	}
	areaBudget := budget / 2
	runBudget := budget - areaBudget
	if len(areaPart) > areaBudget {
		areaPart = areaPart[:areaBudget]
	}
	if len(runID) > runBudget {
		runID = runID[:runBudget]
	}
	return fmt.Sprintf("e2e-%s-%s%s", strings.Trim(areaPart, "-"), strings.Trim(runID, "-"), suffix)
}

// sanitizeSegment converts arbitrary input into DNS-1123-safe runs of
// [a-z0-9-], collapsing and trimming separators.
func sanitizeSegment(in string) string {
	in = strings.ToLower(in)
	var b strings.Builder
	b.Grow(len(in))
	lastDash := false
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
