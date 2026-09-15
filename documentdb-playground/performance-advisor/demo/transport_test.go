//go:build linux

// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package demo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHostCancellationStopsOnlyOwnedLifecycle(t *testing.T) {
	source, err := os.ReadFile("../demo-transport.sh")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "documentdb-playground", "performance-advisor")
	bin := filepath.Join(root, "bin")
	for _, path := range []string{dir, bin} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, contents string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "demo-transport.sh"), string(source))
	write(filepath.Join(dir, "demo.sh"), `#!/bin/bash
set -euo pipefail
sleep 60 &
child=$!
printf '%s\n' "$child" >"$DEMO_FIXTURE_ROOT/child"
trap 'wait "$child" || :; exit 143' TERM
wait "$child"
`)
	write(filepath.Join(bin, "docker"), `#!/bin/bash
set -euo pipefail
[[ "$1" == exec ]] || exit 90
shift
while [[ "$1" == -* ]]; do
  case "$1" in -i) shift ;; --user|--workdir) shift 2 ;; *) exit 91 ;; esac
done
[[ "$1" == fixture-container ]] || exit 92
shift
exec "$@"
`)
	command := exec.Command("bash", filepath.Join(dir, "demo-transport.sh"), "fixture-container", root, "up")
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "DEMO_FIXTURE_ROOT="+root)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		_ = command.Process.Kill()
	})
	var child int
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		data, err := os.ReadFile(filepath.Join(root, "child"))
		if err == nil {
			child, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("fixture lifecycle did not start")
	}
	childProcess, err := os.FindProcess(child)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = childProcess.Kill(); _ = childProcess.Release() })
	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 130 {
			t.Fatalf("interrupted setup did not report cancellation: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancellation did not finish")
	}
	if err := syscall.Kill(child, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("owned lifecycle child survived cancellation: %v", err)
	}
	transports, err := filepath.Glob(filepath.Join(dir, ".demo-transport.*"))
	if err != nil || len(transports) != 0 {
		t.Fatalf("transport state was not removed: %v %v", transports, err)
	}
}
