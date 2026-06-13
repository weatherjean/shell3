//go:build unix

package test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/weatherjean/shell3/internal/store"
)

// TestDelegation_DormantParentRevived is the headline behavioral acceptance test
// for durable subagent delegation: when a child subagent finishes and its
// delegating parent has already exited (is dormant), the parent is revived as a
// background process and processes the child's result under the parent's own
// session id.
//
// Topology: parent A runs once and exits (dormant). Child B runs with
// --parent-session A and exits; in B's doClose it sees A dormant, parks an inbox
// notification, wins ClaimRevive, and spawns `shell3 run --resume A --prompt
// <drained inbox>` in the background (orphaned, surviving B). The revived process
// loads A's history, runs a turn (the drained prompt + assistant ack), and
// persists a NEW turn under A's session id. The proof is message growth on A.
func TestDelegation_DormantParentRevived(t *testing.T) {
	server := fakeAckServer(t)

	homeDir := t.TempDir()
	// Short workdir: the transport opens a unix socket at
	// <workdir>/.shell3/sock/<id>.sock and macOS caps socket paths at ~104 bytes,
	// which t.TempDir() (/var/folders/...) overflows.
	workDir, err := os.MkdirTemp("/tmp", "rev")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	cfgPath := filepath.Join(workDir, "shell3.lua")
	cfg := fmt.Sprintf(`shell3.model("fake", {
  base_url = "%s/v1",
  api_key = "test",
  model = "test-model",
  context_window = 4096,
})
shell3.agent({ name = "tester", model = "fake", prompt = "you are a test", tools = {} })
`, server.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	bin := buildShell3(t)

	run := func(args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %v failed: %v\noutput:\n%s", args, err, out)
		}
	}

	// 1. Parent A: create session, run a turn, persist messages, exit → dormant.
	run("run", "-c", cfgPath, "--agent", "tester", "--id", "a1", "--prompt", "be the parent")

	dbPath := canonicalDB(t, homeDir)

	// Resolve A's session id: the first (only) session in a fresh db.
	parentID := firstSessionID(t, dbPath)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	beforeMsgs, err := st.LoadSessionMessages(parentID)
	if err != nil {
		t.Fatalf("load A messages: %v", err)
	}
	beforeCount := len(beforeMsgs)
	if beforeCount < 2 {
		t.Fatalf("parent A persisted %d messages, want >=2 (user prompt + assistant ack); store not active?", beforeCount)
	}

	// 2. Child B with --parent-session A: runs a turn, exits; on close it sees A
	// dormant → AppendInbox(A) + ClaimRevive(A) won → spawnRevive launches the
	// background revive of A (orphaned, surviving B).
	run("run", "-c", cfgPath, "--agent", "tester", "--parent-session", fmt.Sprint(parentID),
		"--id", "b1", "--prompt", "child task")

	// 3. Poll for revival proof: A's message count grows (revived process loaded A
	// and appended a new turn under A's session id).
	const pollTimeout = 25 * time.Second
	deadline := time.Now().Add(pollTimeout)
	afterCount := beforeCount
	for time.Now().Before(deadline) {
		msgs, lerr := st.LoadSessionMessages(parentID)
		if lerr == nil {
			afterCount = len(msgs)
			if afterCount > beforeCount {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	if afterCount <= beforeCount {
		t.Fatalf("parent A was NOT revived: message count stayed at %d (want > %d) within %s\n%s",
			afterCount, beforeCount, pollTimeout, diagnose(dbPath))
	}

	// A's inbox must be drained by the revive.
	if pending := inboxCount(t, dbPath, parentID); pending != 0 {
		t.Errorf("parent A inbox not drained: %d pending payloads remain", pending)
	}

	t.Logf("revival proven: parent A messages grew %d -> %d (id=%d, db=%s)",
		beforeCount, afterCount, parentID, dbPath)
}

// TestDelegation_CrashedLiveParentRevived proves the crash-strand fix end to
// end: a parent that registered "live" then died WITHOUT cleanup (kill -9 /
// OOM / reboot) leaves a stale "live" row with a dead pid. Before the fix every
// child report to it stranded (ClaimRevive only fired on "dormant"). Now the
// reporter detects the pid is dead, reclaims the row, and revives the parent.
// We simulate the crash by stamping a live row with an unused pid, then run a
// real child process that must revive A (proven by A's message growth).
func TestDelegation_CrashedLiveParentRevived(t *testing.T) {
	server := fakeAckServer(t)
	homeDir := t.TempDir()
	workDir, err := os.MkdirTemp("/tmp", "crash")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	cfgPath := filepath.Join(workDir, "shell3.lua")
	cfg := fmt.Sprintf(`shell3.model("fake", {
  base_url = "%s/v1",
  api_key = "test",
  model = "test-model",
  context_window = 4096,
})
shell3.agent({ name = "tester", model = "fake", prompt = "you are a test", tools = {} })
`, server.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	bin := buildShell3(t)
	run := func(dir string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %v failed: %v\noutput:\n%s", args, err, out)
		}
	}

	// Parent A runs and exits.
	run(workDir, "run", "-c", cfgPath, "--agent", "tester", "--id", "a1", "--prompt", "be the parent")

	dbPath := canonicalDB(t, homeDir)
	parentID := firstSessionID(t, dbPath)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	before, err := st.LoadSessionMessages(parentID)
	if err != nil {
		t.Fatalf("load A messages: %v", err)
	}
	beforeCount := len(before)

	// Simulate a crash: A is stuck "live" with a pid that is not running.
	const deadPID = 2147483646
	sock := filepath.Join(workDir, ".shell3", "sock", fmt.Sprintf("%d.sock", parentID))
	if err := st.SetLiveness(parentID, deadPID, sock, "live"); err != nil {
		t.Fatalf("seed crashed-live state: %v", err)
	}

	// Child B reports to the stuck-live A. The reporter must detect the dead pid,
	// reclaim the row, and revive A.
	run(workDir, "run", "-c", cfgPath, "--agent", "tester",
		"--parent-session", fmt.Sprint(parentID), "--id", "b1", "--prompt", "child task")

	const pollTimeout = 25 * time.Second
	deadline := time.Now().Add(pollTimeout)
	afterCount := beforeCount
	for time.Now().Before(deadline) {
		if msgs, lerr := st.LoadSessionMessages(parentID); lerr == nil {
			afterCount = len(msgs)
			if afterCount > beforeCount {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if afterCount <= beforeCount {
		t.Fatalf("crashed-live parent A was NOT revived: messages stayed at %d (want > %d)\n%s",
			afterCount, beforeCount, diagnose(dbPath))
	}
}

// canonicalDB returns the single per-home database path.
func canonicalDB(t *testing.T, homeDir string) string {
	t.Helper()
	db := filepath.Join(homeDir, ".shell3", "data", "shell3.db")
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("canonical db missing: %v", err)
	}
	return db
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

// roDB opens a read-only *sql.DB on the project file for raw queries that the
// store API does not expose (session listing, status, inbox count).
func roDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		t.Fatalf("open ro db: %v", err)
	}
	return db
}

func firstSessionID(t *testing.T, dbPath string) int64 {
	t.Helper()
	db := roDB(t, dbPath)
	defer db.Close()
	var id int64
	if err := db.QueryRow(`SELECT id FROM sessions ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("read first session id: %v", err)
	}
	return id
}

func inboxCount(t *testing.T, dbPath string, sessionID int64) int {
	t.Helper()
	db := roDB(t, dbPath)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM inbox WHERE session_id = ?`, sessionID).Scan(&n); err != nil {
		t.Fatalf("inbox count: %v", err)
	}
	return n
}

// diagnose dumps the sessions table and per-session message counts for failure
// triage.
func diagnose(dbPath string) string {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return fmt.Sprintf("diagnose: open db: %v", err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT id, COALESCE(parent_session_id, 0), COALESCE(status,''),
		(SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id),
		(SELECT COUNT(*) FROM inbox i WHERE i.session_id = s.id)
		FROM sessions s ORDER BY id`)
	if err != nil {
		return fmt.Sprintf("diagnose: query: %v", err)
	}
	defer rows.Close()
	out := "sessions (id, parent, status, msgs, inbox):\n"
	for rows.Next() {
		var id, parent, msgs, inbox int64
		var status string
		if err := rows.Scan(&id, &parent, &status, &msgs, &inbox); err != nil {
			return out + fmt.Sprintf("  scan err: %v", err)
		}
		out += fmt.Sprintf("  id=%d parent=%d status=%q msgs=%d inbox=%d\n", id, parent, status, msgs, inbox)
	}
	return out
}

// TestDelegation_DivergentChildCwdStillReachesParent proves orchestration is
// CWD-independent: a child launched from a DIFFERENT directory than the parent
// (no shared pwd) still reports to and revives the parent, because both open the
// single canonical DB. Regression test for the silent black-hole bug where a
// divergent launch CWD routed the child's completion into a separate project DB.
func TestDelegation_DivergentChildCwdStillReachesParent(t *testing.T) {
	server := fakeAckServer(t)
	homeDir := t.TempDir()

	workDir, err := os.MkdirTemp("/tmp", "cwdp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })
	otherDir, err := os.MkdirTemp("/tmp", "cwdo") // divergent child launch dir
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(otherDir) })

	cfgPath := filepath.Join(workDir, "shell3.lua")
	cfg := fmt.Sprintf(`shell3.model("fake", {
  base_url = "%s/v1",
  api_key = "test",
  model = "test-model",
  context_window = 4096,
})
shell3.agent({ name = "tester", model = "fake", prompt = "you are a test", tools = {} })
`, server.URL)
	if err := os.WriteFile(cfgPath, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}

	bin := buildShell3(t)
	runIn := func(dir string, args ...string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %v (dir=%s) failed: %v\noutput:\n%s", args, dir, err, out)
		}
	}

	// Parent A in workDir → dormant.
	runIn(workDir, "run", "-c", cfgPath, "--agent", "tester", "--id", "a1", "--prompt", "be the parent")

	dbPath := canonicalDB(t, homeDir)
	parentID := firstSessionID(t, dbPath)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	before, err := st.LoadSessionMessages(parentID)
	if err != nil {
		t.Fatalf("load A messages: %v", err)
	}
	beforeCount := len(before)

	// Child B launched from otherDir (NOT workDir). Must still reach A via the
	// single canonical DB and revive it.
	runIn(otherDir, "run", "-c", cfgPath, "--agent", "tester",
		"--parent-session", fmt.Sprint(parentID), "--id", "b1", "--prompt", "child task")

	const pollTimeout = 25 * time.Second
	deadline := time.Now().Add(pollTimeout)
	afterCount := beforeCount
	for time.Now().Before(deadline) {
		if msgs, lerr := st.LoadSessionMessages(parentID); lerr == nil {
			afterCount = len(msgs)
			if afterCount > beforeCount {
				break
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	if afterCount <= beforeCount {
		t.Fatalf("parent A NOT revived from divergent child CWD: messages stayed at %d (want > %d)\n%s",
			afterCount, beforeCount, diagnose(dbPath))
	}
}
