// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.

package demo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleOnlyDeletesItsRecordedCluster(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("the lifecycle intentionally refuses root execution")
	}
	source, err := os.ReadFile("../demo.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"owned", "missing", "foreign-marker", "foreign-name", "changed-daemon", "replaced-node", "symlink"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			scriptDir := filepath.Join(root, "documentdb-playground", "performance-advisor")
			bin := filepath.Join(root, "bin")
			state := filepath.Join(scriptDir, ".demo")
			for _, dir := range []string{scriptDir, bin} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write := func(path, contents string) {
				t.Helper()
				if err := os.WriteFile(path, []byte(contents), 0700); err != nil {
					t.Fatal(err)
				}
			}
			write(filepath.Join(scriptDir, "demo.sh"), string(source))
			write(filepath.Join(bin, "git"), "#!/bin/bash\nprintf '%s\\n' \"$DEMO_FIXTURE_ROOT\"\n")
			write(filepath.Join(bin, "docker"), `#!/bin/bash
printf 'docker %s\n' "$*" >>"$DEMO_FIXTURE_ROOT/calls"
case "$1" in
  info) printf '%s\n' "$DEMO_FIXTURE_DAEMON" ;;
  ps) if [[ ! -f "$DEMO_FIXTURE_ROOT/deleted" ]]; then printf '%s\n' "$DEMO_FIXTURE_NODE"; fi ;;
  *) exit 90 ;;
esac
`)
			write(filepath.Join(bin, "kind"), `#!/bin/bash
printf 'kind %s\n' "$*" >>"$DEMO_FIXTURE_ROOT/calls"
[[ "$*" == "delete cluster --name telemetry-demo-aaaaaaaaaaaaaaaa" ]] || exit 91
touch "$DEMO_FIXTURE_ROOT/deleted"
`)
			for _, tool := range []string{"kubectl", "jq"} {
				write(filepath.Join(bin, tool), "#!/bin/bash\nexit 92\n")
			}
			if name != "missing" {
				if err := os.Mkdir(state, 0700); err != nil {
					t.Fatal(err)
				}
				write(filepath.Join(state, "owner"), "documentdb-live-telemetry-v1\n")
				write(filepath.Join(state, "cluster"), "telemetry-demo-aaaaaaaaaaaaaaaa\n")
				write(filepath.Join(state, "daemon"), "demo-daemon\n")
				write(filepath.Join(state, "node"), strings.Repeat("a", 64)+"\n")
			}
			daemon, node := "demo-daemon", strings.Repeat("a", 64)
			switch name {
			case "foreign-marker":
				write(filepath.Join(state, "owner"), "other-playground\n")
			case "foreign-name":
				write(filepath.Join(state, "cluster"), "shared-cluster\n")
			case "changed-daemon":
				daemon = "different-daemon"
			case "replaced-node":
				node = strings.Repeat("b", 64)
			case "symlink":
				saved := filepath.Join(root, "unrelated")
				if err := os.Rename(state, saved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(saved, state); err != nil {
					t.Fatal(err)
				}
			}
			command := exec.Command("bash", filepath.Join(scriptDir, "demo.sh"), "down")
			command.Env = append(os.Environ(),
				"PATH="+bin+":"+os.Getenv("PATH"), "WORKSPACE_ROOT="+root,
				"DEMO_FIXTURE_ROOT="+root, "DEMO_FIXTURE_DAEMON="+daemon, "DEMO_FIXTURE_NODE="+node,
			)
			output, err := command.CombinedOutput()
			wantSuccess := name == "owned" || name == "missing"
			if (err == nil) != wantSuccess {
				t.Fatalf("unexpected lifecycle result: %v\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(root, "deleted")); (err == nil) != (name == "owned") {
				t.Fatalf("unexpected cluster deletion for %s: %v", name, err)
			}
			if name == "owned" {
				if _, err := os.Stat(state); !os.IsNotExist(err) {
					t.Fatal("owned runtime state was not removed")
				}
			}
			if name == "symlink" {
				if _, err := os.Stat(filepath.Join(root, "unrelated", "owner")); err != nil {
					t.Fatal("unrelated symlink target was changed")
				}
			}
		})
	}
}
