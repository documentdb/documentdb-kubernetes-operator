package timeouts

import (
	"testing"
	"time"
)

func TestFor_CoversAllOps(t *testing.T) {
	t.Parallel()
	for _, op := range AllOps() {
		d := For(op)
		if d <= 0 {
			t.Fatalf("For(%s) returned non-positive %s", op, d)
		}
		// Guard: every known Op must have an explicit entry in
		// documentDBDefaults (even if its value coincidentally equals
		// UnknownOpFallback) so adding a new Op forces a choice.
		if _, ok := documentDBDefaults[op]; !ok {
			t.Fatalf("Op %s missing from documentDBDefaults — add an explicit default", op)
		}
	}
}

func TestFor_UnknownOpFallback(t *testing.T) {
	t.Parallel()
	got := For(Op("this-op-does-not-exist"))
	if got != UnknownOpFallback {
		t.Fatalf("unknown op: got %s, want %s", got, UnknownOpFallback)
	}
}

func TestFor_DocumentDBUpgrade_IsDocumentDBDefault(t *testing.T) {
	t.Parallel()
	// Not CNPG-aliased → must come straight from documentDBDefaults.
	if got, want := For(DocumentDBUpgrade), 10*time.Minute; got != want {
		t.Fatalf("DocumentDBUpgrade: got %s, want %s", got, want)
	}
}

func TestPollInterval_NonZero(t *testing.T) {
	t.Parallel()
	for _, op := range AllOps() {
		if got := PollInterval(op); got <= 0 {
			t.Fatalf("PollInterval(%s) non-positive: %s", op, got)
		}
	}
	if got := PollInterval(Op("unknown")); got <= 0 {
		t.Fatalf("PollInterval(unknown) non-positive: %s", got)
	}
}
