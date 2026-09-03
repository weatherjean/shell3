//go:build unix

package wrk

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/lispconfig"
)

func TestCompiledBashRunsRalphLoopEndToEnd(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	configSource := `(shell3
  (version 1)
  (runner fake (command "/fake") (result stdout))
  (agent builder (using fake)))`
	if err := os.WriteFile(configPath, []byte(configSource), 0o600); err != nil {
		t.Fatal(err)
	}
	wrkPath := filepath.Join(dir, "demo.wrk.lisp")
	wrkSource := `(task "demo"
  (root ".")
  (loop implement
    (using builder)
    (max 3)
    (prompt "Do one increment.")
    (until (sh "test -f \"$TASK_ARTIFACTS/done\""))))`
	if err := os.WriteFile(wrkPath, []byte(wrkSource), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	definition, err := Load(wrkPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	script, err := Compile(definition, configPath)
	if err != nil {
		t.Fatal(err)
	}
	compiled := filepath.Join(dir, "demo.task.sh")
	if err := os.WriteFile(compiled, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", compiled).CombinedOutput(); err != nil {
		t.Fatalf("bash -n: %v\n%s", err, out)
	}

	fake := filepath.Join(dir, "fake-shell3.sh")
	fakeSource := `#!/usr/bin/env bash
set -euo pipefail
if [ "$1" = notify ]; then
  printf '%s\n' "$@" > "$NOTIFY_CAPTURE"
  printf '{"persisted":true,"wake":"unavailable"}\n'
  exit 0
fi
[ "$1" = wrk ] && [ "$2" = _agent ]
shift 2
run_dir=
artifacts=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --run-dir) run_dir=$2; shift 2 ;;
    --slot)
      case "$2" in task-artifacts=*) artifacts=${2#task-artifacts=} ;; esac
      shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "$run_dir" "$artifacts"
cat > "$run_dir/received-prompt.md"
case "$run_dir" in *attempt-2) touch "$artifacts/done" ;; esac
printf 'fake result\n'
`
	if err := os.WriteFile(fake, []byte(fakeSource), 0o700); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "state")
	notifyCapture := filepath.Join(dir, "notify.args")
	cmd := exec.Command("bash", compiled, "Build the demo")
	cmd.Env = append(os.Environ(), "SHELL3_BIN="+fake, "SHELL3_WRK_STATE="+state, "SHELL3_WRK_RUN_ID=test-run",
		"SHELL3_WRK_NOTIFY_TO=session:owner", "SHELL3_WRK_NOTIFY_STATE="+filepath.Join(dir, "messages"), "NOTIFY_CAPTURE="+notifyCapture)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compiled workflow: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "completed demo (test-run)") {
		t.Fatalf("output = %q", out)
	}
	status, err := os.ReadFile(filepath.Join(state, "demo", "test-run", "nodes", "implement", "status"))
	if err != nil || strings.TrimSpace(string(status)) != "passed" {
		t.Fatalf("status = %q, err = %v", status, err)
	}
	prompt, err := os.ReadFile(filepath.Join(state, "demo", "test-run", "nodes", "implement", "attempt-2", "received-prompt.md"))
	if err != nil || !strings.Contains(string(prompt), "Original request:\nBuild the demo") {
		t.Fatalf("prompt = %q, err = %v", prompt, err)
	}
	notification, err := os.ReadFile(notifyCapture)
	if err != nil || !strings.Contains(string(notification), "wrk.completed") || !strings.Contains(string(notification), "session:owner") {
		t.Fatalf("notification args = %q, err = %v", notification, err)
	}
}

func TestExecutionWavesParallelizeReadersAndSerializeWriter(t *testing.T) {
	definition := &Definition{Parallel: 3, Nodes: []Node{
		{Name: "read-a", Access: "read"},
		{Name: "read-b", Access: "read"},
		{Name: "write", Access: "write", After: []string{"read-a", "read-b"}},
		{Name: "finish", Access: "read", After: []string{"write"}},
	}}
	waves, err := executionWaves(definition)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]int{{0, 1}, {2}, {3}}
	if len(waves) != len(want) {
		t.Fatalf("waves = %#v", waves)
	}
	for i := range want {
		if len(waves[i]) != len(want[i]) {
			t.Fatalf("waves = %#v", waves)
		}
		for j := range want[i] {
			if waves[i][j] != want[i][j] {
				t.Fatalf("waves = %#v", waves)
			}
		}
	}
}
