//go:build unix

package test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/weatherjean/shell3/internal/store"
)

// TestConfigPerSession_ResumeUsesRecordedConfig is the headline acceptance test
// for config-per-session: resuming a session with NO --config flag must pick up
// the config_path that was recorded when the session was created.
//
// The proof is structural. The resume is launched from an EMPTY cwd under an
// EMPTY temp HOME, so default config resolution (./shell3.lua, then
// ~/.shell3/shell3.lua) finds NOTHING — there is no fallback config anywhere on
// the resolution path. If the resume nonetheless succeeds and appends a turn,
// the ONLY config it could have loaded is the session's recorded config_path
// (cfgA). A regression that drops the recorded config would make this run fail
// with "no config found", so a green run is the feature working.
func TestConfigPerSession_ResumeUsesRecordedConfig(t *testing.T) {
	server := fakeAckServer(t)

	homeDir := t.TempDir() // NO ~/.shell3/shell3.lua under this temp HOME.

	// Short workdir: socket paths created during a run are capped at ~104 bytes
	// on macOS, which t.TempDir() (/var/folders/...) would overflow.
	workDir, err := os.MkdirTemp("/tmp", "cfgA")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	// Config A lives at a real, resolvable absolute path inside workDir.
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

	// 1. Create the session under config A. This records config_path = cfgA.
	out, err := run(workDir, "run", "-c", cfgA, "--agent", "tester", "--id", "a1", "--prompt", "be the parent")
	if err != nil {
		t.Fatalf("seed run failed: %v\n%s", err, out)
	}

	dbPath := canonicalDB(t, homeDir)
	id := firstSessionID(t, dbPath)

	// Sanity: the DB actually recorded cfgA for this session.
	var recorded string
	db := roDB(t, dbPath)
	defer db.Close()
	if err := db.QueryRow(`SELECT config_path FROM sessions WHERE id = ?`, id).Scan(&recorded); err != nil {
		t.Fatalf("read recorded config_path: %v", err)
	}
	if recorded != cfgA {
		t.Fatalf("recorded config_path = %q, want %q", recorded, cfgA)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	before, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatalf("load messages before resume: %v", err)
	}
	beforeN := len(before)

	// 2. Resume from an EMPTY dir with NO --config. Default resolution finds no
	// shell3.lua (no ./shell3.lua in emptyDir, no ~/.shell3/shell3.lua under the
	// temp HOME), so success here PROVES the recorded config_path (cfgA) was used.
	emptyDir, err := os.MkdirTemp("/tmp", "empty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(emptyDir) })

	out, err = run(emptyDir, "run", "--resume", fmt.Sprint(id), "--prompt", "carry on")
	if err != nil {
		t.Fatalf("resume WITHOUT --config failed — recorded config not used?\n%s", out)
	}

	// 3. Prove the resume actually ran a turn under A: A's message count grew.
	after, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatalf("load messages after resume: %v", err)
	}
	if len(after) <= beforeN {
		t.Fatalf("resume did not append a turn (msgs %d -> %d); config A may not have run", beforeN, len(after))
	}

	t.Logf("recorded-config resume proven: msgs %d -> %d under cfgA=%s (empty cwd + empty HOME ⇒ no fallback config)",
		beforeN, len(after), cfgA)
}

// TestConfigPerSession_FTSExcludesToolTurns proves at the CLI level that
// `shell3 fts` searches only user/assistant turns, never tool turns. We seed the
// canonical DB directly with one user turn and one tool turn that share a unique
// query token, then run the real binary and assert the tool turn's content is
// absent while the user turn is present.
func TestConfigPerSession_FTSExcludesToolTurns(t *testing.T) {
	homeDir := t.TempDir()
	dbPath := filepath.Join(homeDir, ".shell3", "data", "shell3.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	const projUUID = "proj-uuid"
	id, err := st.StartSession(projUUID, "/w", "") // 3rd arg = configPath (now required).
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if err := st.AppendHistory(id, "user", "find zebraterm here"); err != nil {
		t.Fatalf("append user turn: %v", err)
	}
	if err := st.AppendHistory(id, "tool", "bash: $ echo zebraterm"); err != nil {
		t.Fatalf("append tool turn: %v", err)
	}
	// Close before the subprocess reads so the WAL is flushed to the main db file.
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	bin := buildShell3(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "fts", "zebraterm", "--project-id", projUUID)
	cmd.Env = append(os.Environ(), "HOME="+homeDir)
	rawOut, err := cmd.CombinedOutput()
	out := string(rawOut)
	if err != nil {
		t.Fatalf("fts failed: %v\n%s", err, out)
	}

	// The user turn must be present (role "user" + its content).
	if !strings.Contains(out, "find zebraterm here") {
		t.Fatalf("fts output missing the user turn; got:\n%s", out)
	}
	if !strings.Contains(out, "user") {
		t.Fatalf("fts output missing role \"user\"; got:\n%s", out)
	}
	// The tool turn must be ABSENT: neither its role nor its unique content.
	if strings.Contains(out, "echo zebraterm") {
		t.Fatalf("fts leaked tool-turn content (echo zebraterm); tool turns must be excluded:\n%s", out)
	}
	if strings.Contains(out, "tool") {
		t.Fatalf("fts leaked a tool role; tool turns must be excluded:\n%s", out)
	}

	t.Logf("fts tool-exclusion proven; output:\n%s", out)
}
