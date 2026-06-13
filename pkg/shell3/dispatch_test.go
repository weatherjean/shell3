package shell3

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// writeStubBinary writes a small shell script that mimics `shell3 --out <t>
// "<prompt>"`: it locates the --out path in its args, writes a transcript with
// a final assistant_message (echoing the prompt) and an end status, then exits.
// failStatus=true makes it write status:"error" so the dispatch failure path is
// exercised. Returns the script path.
func writeStubBinary(t *testing.T, finalText string, failStatus bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub shell binary is POSIX-only")
	}
	end := `{"kind":"end","status":"ok"}`
	if failStatus {
		end = `{"kind":"end","status":"error"}`
	}
	// $@ holds the dispatch args. The real shell3 only accepts --out under the
	// `run` subcommand, so the stub asserts `run` is the first arg (exit 2
	// otherwise) — this catches a dispatch that forgets the subcommand, which the
	// real binary rejects with "unknown flag: --out". Then we scan for --out.
	script := `#!/bin/sh
if [ "$1" != "run" ]; then
  echo "stub: expected 'run' subcommand, got '$1'" >&2
  exit 2
fi
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--out" ]; then out="$a"; fi
  prev="$a"
done
mkdir -p "$(dirname "$out")"
printf '%s\n' '{"kind":"assistant_message","text":"` + finalText + `"}' > "$out"
printf '%s\n' '` + end + `' >> "$out"
`
	path := filepath.Join(t.TempDir(), "stub-shell3.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDispatch_SubprocessEmitsNotice drives the cron path: Dispatch execs the
// (stubbed) shell3 subprocess, reads the final assistant text from the
// transcript it wrote, and emits a Notice (not a Wake) when Notify is true.
func TestDispatch_SubprocessEmitsNotice(t *testing.T) {
	stub := writeStubBinary(t, "cron result", false)
	old := shell3Binary
	shell3Binary = func() string { return stub }
	t.Cleanup(func() { shell3Binary = old })

	rt := newTestRuntime(t, fakeCfg("ok"))
	// ConfigPath() returns the flag verbatim when non-empty; the stub ignores it.
	rt.configPath = filepath.Join(t.TempDir(), "shell3.lua")
	if err := os.WriteFile(rt.configPath, []byte("-- stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := rt.Session(SessionOpts{Name: "main", WorkDir: rt.workDir})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Dispatch("tester", "do nightly", DispatchOpts{Label: "cron:nightly", Notify: true}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind == Notice {
				if ev.Session != "main" {
					t.Errorf("Notice session = %q, want main", ev.Session)
				}
				if want := "[cron:nightly] cron result"; ev.Text != want {
					t.Errorf("Notice text = %q, want %q", ev.Text, want)
				}
				return
			}
		case <-deadline:
			t.Fatal("no Notice delivered for the cron dispatch")
		}
	}
}

// TestDispatch_FailureAlwaysNotifies asserts that a failed run (non-ok end
// status) delivers a Notice even when Notify is false — a quiet job can never
// fail silently.
func TestDispatch_FailureAlwaysNotifies(t *testing.T) {
	stub := writeStubBinary(t, "partial work", true)
	old := shell3Binary
	shell3Binary = func() string { return stub }
	t.Cleanup(func() { shell3Binary = old })

	rt := newTestRuntime(t, fakeCfg("ok"))
	rt.configPath = filepath.Join(t.TempDir(), "shell3.lua")
	if err := os.WriteFile(rt.configPath, []byte("-- stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := rt.Session(SessionOpts{Name: "main", WorkDir: rt.workDir})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Dispatch("tester", "quiet job", DispatchOpts{Label: "cron:q", Notify: false}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind == Notice {
				return // a Notice arrived despite Notify=false — failure path works
			}
		case <-deadline:
			t.Fatal("a failed quiet dispatch did not notify")
		}
	}
}

// TestDispatch_StaleTranscriptNotReused guards the stale-read defect: the
// per-process id scheme (a1, a2, …) resets each launch, so a transcript path can
// collide with a leftover file from a PRIOR run. If the child fails without
// writing, the parent must NOT deliver that stale file's content as this run's
// result. Reproduces the "[cron:smoke] <old main.go report>" symptom.
func TestDispatch_StaleTranscriptNotReused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub shell binary is POSIX-only")
	}
	// A stub that fails immediately and writes nothing (a child that dies early).
	stubPath := filepath.Join(t.TempDir(), "fail.sh")
	if err := os.WriteFile(stubPath, []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := shell3Binary
	shell3Binary = func() string { return stubPath }
	t.Cleanup(func() { shell3Binary = old })

	rt := newTestRuntime(t, fakeCfg("ok"))
	rt.configPath = filepath.Join(t.TempDir(), "shell3.lua")
	if err := os.WriteFile(rt.configPath, []byte("-- stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := rt.Session(SessionOpts{Name: "main", WorkDir: rt.workDir})
	if err != nil {
		t.Fatal(err)
	}

	// Pre-seed the transcript path the first dispatch will use (id "a1") with
	// stale content from a "previous run".
	stale := filepath.Join(rt.root(), ".shell3", "agents", "a1.jsonl")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte(`{"kind":"assistant_message","text":"STALE REPORT"}`+"\n{\"kind\":\"end\",\"status\":\"ok\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Dispatch("tester", "do work", DispatchOpts{Label: "cron:x", Notify: true}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind == Notice {
				if strings.Contains(ev.Text, "STALE REPORT") {
					t.Fatalf("delivered stale transcript content: %q", ev.Text)
				}
				return // an error notice (not the stale report) — correct
			}
		case <-deadline:
			t.Fatal("no Notice delivered")
		}
	}
}

// TestDispatch_EmptyAgentErrors asserts the input guard.
func TestDispatch_EmptyAgentErrors(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Dispatch("  ", "x", DispatchOpts{}); err == nil {
		t.Fatal("Dispatch with empty agent should error")
	}
}
