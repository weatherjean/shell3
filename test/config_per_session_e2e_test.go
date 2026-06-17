//go:build unix

package test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestConfigPerSession_ResumeUsesRecordedConfig is the headline acceptance test
// for config-per-session: resuming a session with NO --config flag must pick up
// the config_path that was recorded when the session was created.
//
// The proof is structural. Default config resolution (ResolveConfigPath) looks
// ONLY at ~/.shell3/shell3.lua — it does NOT consult the cwd. The resume runs
// under an EMPTY temp HOME, so there is no fallback config anywhere on the
// default resolution path. If the resume nonetheless succeeds and appends a
// turn, the ONLY config it could have loaded is the session's recorded
// config_path (cfgA), read from the run's meta.json. A regression that drops the
// recorded config would make this run fail with "no config found", so a green
// run is the feature working.
//
// In the file-native model runs live under <cwd>/.shell3_project/runs/, so the
// resume MUST be launched from the SAME cwd (workDir) that created the session —
// that is how `shell3 run --resume <id>` finds the run's meta.json. (The old
// single-canonical-DB design allowed resuming from any cwd; that no longer
// exists.)
func TestConfigPerSession_ResumeUsesRecordedConfig(t *testing.T) {
	server := fakeAckServer(t)

	homeDir := t.TempDir() // NO ~/.shell3/shell3.lua under this temp HOME.

	workDir, err := os.MkdirTemp("/tmp", "cfgA")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	// Config A lives at a real, resolvable absolute path inside workDir. Note that
	// default resolution does NOT look in cwd, so its presence here is irrelevant
	// to the proof — only the recorded config_path can load it on resume.
	cfgA := filepath.Join(workDir, "shell3.lua")
	cfg := fmt.Sprintf(`shell3.model("fake", {
  base_url = "%s/v1",
  api_key = "test",
  model = "test-model",
  context_window = 4096,
})
shell3.agent({ name = "tester", model = "fake", prompt = "you are a test", tools = {} })
`, server.URL)
	if err := os.WriteFile(cfgA, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	bin := buildShell3(t)
	run := func(dir string, args ...string) (string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// 1. Create the session under config A. This records config_path = cfgA in the
	// run's meta.json.
	out, err := run(workDir, "run", "-c", cfgA, "--agent", "tester", "--id", "a1", "--prompt", "be the parent")
	if err != nil {
		t.Fatalf("seed run failed: %v\n%s", err, out)
	}

	// Resolve the session id and recorded config from the file-native runs store
	// rooted at workDir/.shell3_project/.
	local := paths.NewLocal(workDir)
	st, err := runs.Open(local.Root)
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	metas, err := st.ListSessions(0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("want exactly 1 session in fresh project, got %d", len(metas))
	}
	id := metas[0].ID

	// Sanity: the run actually recorded cfgA for this session.
	if metas[0].ConfigPath != cfgA {
		t.Fatalf("recorded config_path = %q, want %q", metas[0].ConfigPath, cfgA)
	}

	before, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("load messages before resume: %v", err)
	}
	beforeN := len(before)
	if beforeN < 2 {
		t.Fatalf("seed run persisted %d messages, want >=2 (user prompt + assistant ack)", beforeN)
	}

	// 2. Resume from workDir (same cwd, so the run is found) with NO --config.
	// Default resolution finds no shell3.lua (no ~/.shell3/shell3.lua under the
	// temp HOME, and resolution never consults cwd), so success here PROVES the
	// recorded config_path (cfgA) was used.
	out, err = run(workDir, "run", "--resume", id, "--prompt", "carry on")
	if err != nil {
		t.Fatalf("resume WITHOUT --config failed — recorded config not used?\n%s", out)
	}

	// 3. Prove the resume actually ran a turn under A: A's message count grew.
	after, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("load messages after resume: %v", err)
	}
	if len(after) <= beforeN {
		t.Fatalf("resume did not append a turn (msgs %d -> %d); config A may not have run", beforeN, len(after))
	}

	t.Logf("recorded-config resume proven: msgs %d -> %d under cfgA=%s (empty HOME ⇒ no fallback config)",
		beforeN, len(after), cfgA)
}

// fakeAckServer returns a stateless SSE server returning an "ack" chat-completion.
func fakeAckServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":"ack"},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		}
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
			if flusher != nil {
				flusher.Flush()
			}
		}
		fmt.Fprintf(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// buildShell3 builds ./cmd/shell3 to a temp path and returns the binary path.
func buildShell3(t *testing.T) string {
	t.Helper()
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "shell3")
	build := exec.Command("go", "build", "-o", bin, "./cmd/shell3")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}
