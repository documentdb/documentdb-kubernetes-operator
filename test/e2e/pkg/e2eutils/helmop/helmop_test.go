package helmop

import (
	"context"
	"testing"
	"time"
)

func TestSetFlagsDeterministic(t *testing.T) {
	t.Parallel()
	got := setFlags(map[string]string{"b": "2", "a": "1", "c": "3"})
	want := []string{"--set", "a=1", "--set", "b=2", "--set", "c=3"}
	if len(got) != len(want) {
		t.Fatalf("setFlags length = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("setFlags[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSetFlagsEmpty(t *testing.T) {
	t.Parallel()
	if got := setFlags(nil); got != nil {
		t.Fatalf("setFlags(nil) = %v, want nil", got)
	}
	if got := setFlags(map[string]string{}); got != nil {
		t.Fatalf("setFlags(empty) = %v, want nil", got)
	}
}

func TestInstallRejectsEmptyArgs(t *testing.T) {
	t.Parallel()
	cases := []struct{ rel, ns, chart string }{
		{"", "ns", "chart"},
		{"rel", "", "chart"},
		{"rel", "ns", ""},
	}
	for _, c := range cases {
		if err := Install(context.Background(), c.rel, c.ns, c.chart, "", nil); err == nil {
			t.Errorf("Install(%q,%q,%q) = nil, want error", c.rel, c.ns, c.chart)
		}
	}
}

func TestUpgradeRejectsEmptyArgs(t *testing.T) {
	t.Parallel()
	if err := Upgrade(context.Background(), "", "ns", "chart", "", nil); err == nil {
		t.Error("Upgrade with empty release = nil, want error")
	}
}

func TestUninstallRejectsEmptyArgs(t *testing.T) {
	t.Parallel()
	if err := Uninstall(context.Background(), "", "ns"); err == nil {
		t.Error("Uninstall with empty release = nil, want error")
	}
	if err := Uninstall(context.Background(), "rel", ""); err == nil {
		t.Error("Uninstall with empty namespace = nil, want error")
	}
}

func TestWaitOperatorReadyNilEnv(t *testing.T) {
	t.Parallel()
	if err := WaitOperatorReady(context.Background(), nil, "ns", time.Millisecond); err == nil {
		t.Error("WaitOperatorReady(nil env) = nil, want error")
	}
}
