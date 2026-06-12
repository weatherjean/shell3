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
	// Stateless fake OpenAI-compat server: every request returns the same SSE
	// chat-completion ("ack"). Serves A's turn, B's turn, and the revived turn.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	defer server.Close()

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

	// Build a fresh binary so we test HEAD, not a stale repo-root ./shell3.
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

	dbPath := findProjectDB(t, homeDir)

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

// findProjectDB globs the single project db under <home>/.shell3/projects/.
func findProjectDB(t *testing.T, homeDir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(homeDir, ".shell3", "projects", "*", "shell3.db"))
	if err != nil {
		t.Fatalf("glob project db: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one project db, found %d: %v", len(matches), matches)
	}
	return matches[0]
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
