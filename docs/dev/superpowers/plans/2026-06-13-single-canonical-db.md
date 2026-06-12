# Single Canonical DB Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-project SQLite database files (`~/.shell3/projects/<uuid>/shell3.db`) with a single canonical database (`~/.shell3/shell3.db`) namespaced by a `project_uuid` column, so cross-process agent orchestration (parent/child reports, liveness, inbox, revive) is independent of the process working directory.

**Architecture:** One SQLite file per home, at `~/.shell3/data/shell3.db`. Conversation history stays logically per-project via a `project_uuid` column on `sessions` and on the `history` FTS5 table. The `.shell3/.ref` → UUID mapping survives only as a namespacing key, no longer a filesystem path component. Orchestration routing needs nothing but the canonical DB path + a globally-unique session id; a wrong CWD can at worst mislabel a child's own history rows, never black-hole its completion report. Full-text history is exposed to the agent through a first-class read-only command, `shell3 fts <query> [--project-id <uuid>] [--page N]` (project-id optional → omit to search across all projects), instead of hand-written `sqlite3 … MATCH …` — read-only by construction and bash-composable. Project enumeration (previously `ls ~/.shell3/projects/`, now gone) is replaced by `shell3 list-projects [--page N]`, reading `DISTINCT project_uuid` from the single DB with each project's workdir and last activity — this is what lets the TUI/`--resume` and the telegram front-end discover and resume sessions across projects, since all front-ends share the one `~/.shell3/data/shell3.db`. Clean break: the per-project-dir / `meta.json` / `FindByCWD` recovery machinery is deleted outright — **no backwards-compat migration**.

**Tech Stack:** Go, `modernc.org/sqlite` (WAL), SQLite FTS5.

**Why (root cause this fixes):** The project DB *and* the per-session socket were both re-derived from the launch CWD (`<cwd>/.shell3/.ref` → DB; `SockPath(cwd,…)`). `bash_bg` lets the model choose a child's launch directory, so a divergent CWD made the child open a *different* project DB where the parent session did not exist → `Liveness(parentID)` ErrNoRows → "dormant" → `ClaimRevive(parentID)` 0 rows → `won=false, err=nil` → silently treated as "a winner will deliver" when there is no winner. The completion report was black-holed with no error logged.

---

## File Structure

| File | Responsibility | Change |
|------|----------------|--------|
| `internal/store/store.go` | schema + FTS history insert + StartSession + search | add `project_uuid`/`workdir` to `sessions`, `project_uuid` to `history` FTS, subselect on insert, new StartSession signature; extend `HistorySearchExpr` with project filter + paging |
| `internal/store/sessions.go` | StartSessionWithParent + ListProjects | new signature carrying project_uuid + workdir; add `ListProjects(limit, offset)` |
| `cmd/shell3/fts.go` | `shell3 fts` command | **new**: paginated, project-scoped FTS over the canonical DB |
| `cmd/shell3/projects.go` | `shell3 list-projects` command | **new**: paginated `DISTINCT project_uuid` listing |
| `internal/paths/paths.go` | path resolution | add `Global.Data` + `Global.DB` (under `data/`); **delete** `Project`/`NewProject`/`Global.Projects` |
| `internal/ref/ref.go` | `.ref` UUID minting | **delete** `Meta`/`ReadMeta`/`FindByCWD`/`writeMeta` + project-dir/meta logic; `Init` just mints+writes `.ref` |
| `internal/bootstrap/bootstrap.go` | first-run setup | `EnsureGlobal` drops `Projects` mkdir; `EnsureProject` drops project-dir mkdir; gitignore drops `projects/` |
| `internal/agentsetup/agentsetup.go` | shared assembly | open `g.DB`; pass project_uuid+workdir to session start; add `project_uuid` to Environment section |
| `pkg/shell3/shell3.go` | session lifecycle | pass `cfg.ProjectRef`, `cfg.WorkDir` to StartSession(WithParent) |
| `internal/chat/tools.go` | `/clear` new session | pass project_uuid + workdir to StartSession |
| `internal/scaffold/defaults/base/lib/skills/history.md` | history skill doc | document `project_uuid`; scope canonical queries to the agent's project |
| `test/delegation_e2e_test.go` | e2e | canonical-DB helper; replace `--cwd` draft with divergent-CWD regression test |

---

## Task 1: Schema — project_uuid + workdir columns, per-project FTS

**Files:**
- Modify: `internal/store/store.go` (migrate ~56-98, history insert ~213-216, StartSession ~111-124)
- Modify: `internal/store/sessions.go` (StartSessionWithParent ~9-19)
- Test: `internal/store/schema_test.go`, `internal/store/sessions_test.go`

- [ ] **Step 1: Write the failing test** — add to `internal/store/sessions_test.go`:

```go
func TestStartSession_RecordsProjectAndWorkdir(t *testing.T) {
	st := openTestStore(t) // existing helper; if absent use store.Open(filepath.Join(t.TempDir(),"s.db"))
	id, err := st.StartSession("proj-abc", "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	uuid, wd := projectAndWorkdir(t, st, id)
	if uuid != "proj-abc" || wd != "/tmp/work" {
		t.Fatalf("got (%q,%q), want (proj-abc,/tmp/work)", uuid, wd)
	}
}

// projectAndWorkdir reads the two new columns directly.
func projectAndWorkdir(t *testing.T, st *store.Store, id int64) (string, string) {
	t.Helper()
	var uuid, wd string
	if err := st.DB().QueryRow(
		`SELECT project_uuid, workdir FROM sessions WHERE id=?`, id).Scan(&uuid, &wd); err != nil {
		t.Fatal(err)
	}
	return uuid, wd
}
```

If `store.Store` has no `DB()` accessor, add one (`func (s *Store) DB() *sql.DB { return s.db }`) — already used by other tests if present; check first.

- [ ] **Step 2: Run test, verify it fails**

Run: `go test ./internal/store/ -run TestStartSession_RecordsProjectAndWorkdir -v`
Expected: FAIL — compile error (StartSession takes 0 args) or "no such column: project_uuid".

- [ ] **Step 3: Migrate the schema** — in `internal/store/store.go` `migrate()`, replace the `sessions` and `history` statements:

```go
`CREATE TABLE IF NOT EXISTS sessions (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at        TEXT NOT NULL,
	ended_at          TEXT,
	summary           TEXT,
	parent_session_id INTEGER,
	pid               INTEGER NOT NULL DEFAULT 0,
	sock              TEXT NOT NULL DEFAULT '',
	status            TEXT NOT NULL DEFAULT 'dormant',
	project_uuid      TEXT NOT NULL DEFAULT '',
	workdir           TEXT NOT NULL DEFAULT ''
)`,
```

```go
`CREATE VIRTUAL TABLE IF NOT EXISTS history USING fts5(
	content,
	session_id   UNINDEXED,
	role         UNINDEXED,
	created_at   UNINDEXED,
	project_uuid UNINDEXED
)`,
```

Note: clean break — no `ALTER TABLE`; a fresh DB is created with the full schema. (Old per-project DBs are abandoned, not migrated.)

- [ ] **Step 4: New StartSession signatures**

`internal/store/store.go`:

```go
// StartSession inserts a new session row tagged with its project + workdir and
// returns its id.
func (s *Store) StartSession(projectUUID, workdir string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`INSERT INTO sessions(started_at, project_uuid, workdir) VALUES(?,?,?)`,
		now, projectUUID, workdir)
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("store: start session: last insert id: %w", err)
	}
	return id, nil
}
```

`internal/store/sessions.go`:

```go
// StartSessionWithParent inserts a new session row whose parent_session_id
// records the report pointer, tagged with its project + workdir.
func (s *Store) StartSessionWithParent(parent int64, projectUUID, workdir string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`INSERT INTO sessions(started_at, parent_session_id, project_uuid, workdir) VALUES(?,?,?,?)`,
		now, parent, projectUUID, workdir)
	if err != nil {
		return 0, fmt.Errorf("store: start session with parent: %w", err)
	}
	return res.LastInsertId()
}
```

- [ ] **Step 5: Stamp project_uuid into the FTS history insert** — `internal/store/store.go` (~line 213). The writer only has `sessionID`, so derive the project via subselect (no signature change):

```go
	`INSERT INTO history(content, session_id, role, created_at, project_uuid)
	 VALUES(?, ?, ?, ?, (SELECT project_uuid FROM sessions WHERE id = ?))`,
```

Add the trailing `sessionID` argument to the `Exec` call to fill the subselect bind. Verify the exact call site arguments while editing.

- [ ] **Step 6: Run store tests, verify pass**

Run: `go test ./internal/store/ -v`
Expected: PASS (new test green; fix any existing store tests calling the old `StartSession()`/`StartSessionWithParent(parent)` signatures by threading `""`/`"/tmp"` or real values).

- [ ] **Step 7: Commit**

```bash
git add internal/store/
git commit -m "feat(store): project_uuid + workdir columns, per-project FTS history"
```

---

## Task 2: Canonical DB path; delete per-project-dir / meta machinery

**Files:**
- Modify: `internal/paths/paths.go`
- Modify: `internal/ref/ref.go`
- Modify: `internal/bootstrap/bootstrap.go`
- Test: `internal/ref/ref_test.go`, `internal/bootstrap/bootstrap_test.go`

- [ ] **Step 1: Write the failing test** — `internal/ref/ref_test.go`:

```go
func TestInit_MintsRefWithoutProjectDir(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	if err := os.MkdirAll(l.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := ref.Init(l, g)
	if err != nil {
		t.Fatal(err)
	}
	if id == "" {
		t.Fatal("empty uuid")
	}
	// No per-project directory is created anymore.
	if _, err := os.Stat(filepath.Join(home, ".shell3", "projects")); !os.IsNotExist(err) {
		t.Fatalf("projects/ dir should not exist, stat err=%v", err)
	}
	// Idempotent: second call returns the same id.
	id2, _ := ref.Init(l, g)
	if id2 != id {
		t.Fatalf("non-idempotent: %q != %q", id2, id)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/ref/ -run TestInit_MintsRefWithoutProjectDir -v`
Expected: FAIL — `ref.Init` currently takes `(l, g, cwd)` (arity/compile error).

- [ ] **Step 3: Add canonical DB path; delete Project paths** — `internal/paths/paths.go`:

In `Global`, add `Data string // ~/.shell3/data/` and `DB string // ~/.shell3/data/shell3.db`, and **remove** the `Projects` field. In `NewGlobal`:

```go
func NewGlobal(homeDir string) Global {
	root := filepath.Join(homeDir, ".shell3")
	data := filepath.Join(root, "data")
	return Global{
		Root:    root,
		Data:    data,
		DB:      filepath.Join(data, "shell3.db"),
		LogFile: filepath.Join(root, "shell3.log"),
	}
}
```

**Delete** the `Project` struct and `NewProject` function entirely.

- [ ] **Step 4: Simplify ref.go** — replace the body of `internal/ref/ref.go` so it only mints/reads `.ref`. **Delete** `Meta`, `ReadMeta`, `FindByCWD`, `writeMeta`, the project-dir mkdir, and the `cwd`/`g` recovery logic. Keep `Load`, `writeRefExcl`, `reloadWinner`:

```go
// Init returns the project UUID for this cwd, minting and writing .ref on first
// use. Idempotent. The UUID is now purely a namespacing key for the single
// canonical DB — it is no longer a directory name, so no project dir or meta is
// created. O_EXCL serialises concurrent first-use in the same cwd.
func Init(l paths.Local, g paths.Global) (string, error) {
	if id, err := Load(l); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	id := uuid.New().String()
	if err := writeRefExcl(l.Ref, id); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return reloadWinner(l) // lost the create race; trust the winner
		}
		return "", err
	}
	return id, nil
}
```

Drop now-unused imports (`encoding/json`, `io/fs` stays for ErrExist, `path/filepath`, `time`). Keep `errors`, `os`, `strings`, `github.com/google/uuid`, `paths`. (Confirm `fs` still used by `errors.Is(err, fs.ErrExist)`.)

- [ ] **Step 5: Update bootstrap** — `internal/bootstrap/bootstrap.go`:

`EnsureGlobal`: mkdir `g.Root` and `g.Data` (replace the old `g.Projects` entry with `g.Data`):

```go
	for _, dir := range []string{g.Root, g.Data} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
	}
```

`EnsureProject`: drop `paths.NewProject` + project-dir mkdir; call `ref.Init(l, g)`:

```go
func EnsureProject(l paths.Local, g paths.Global, cwd string) (string, error) {
	if err := os.MkdirAll(l.Root, 0755); err != nil {
		return "", fmt.Errorf("bootstrap: mkdir %s: %w", l.Root, err)
	}
	if err := ensureGitignore(l); err != nil {
		return "", err
	}
	id, err := ref.Init(l, g)
	if err != nil {
		return "", fmt.Errorf("bootstrap: ref init: %w", err)
	}
	return id, nil
}
```

Keep the `cwd` parameter in `EnsureProject`'s signature only if a caller still passes it; otherwise drop it and update `agentsetup` (Task 3). **Recommended: drop it** — it's now unused. In `globalGitignoreAddition`, replace the `projects/` line with `data/` (the DB dir is the new never-commit path).

- [ ] **Step 6: Run, verify pass**

Run: `go test ./internal/ref/ ./internal/bootstrap/ ./internal/paths/ -v`
Expected: PASS (update bootstrap_test/ref_test that referenced meta/FindByCWD/project dir).

- [ ] **Step 7: Commit**

```bash
git add internal/paths/ internal/ref/ internal/bootstrap/
git commit -m "refactor: canonical ~/.shell3/shell3.db path; delete per-project dir + meta machinery"
```

---

## Task 3: agentsetup wiring — open canonical DB, expose project_uuid

**Files:**
- Modify: `internal/agentsetup/agentsetup.go` (resolvePaths ~371-389, openStore ~423-430, BuildParts ~335-336, environmentSection ~228-238)
- Test: `internal/agentsetup/*_test.go` if present, else covered by e2e (Task 6)

- [ ] **Step 1: resolvePaths — drop NewProject, keep uuid**

```go
func (b *builder) resolvePaths() error {
	configPath, err := ResolveConfigPath(b.opts.ConfigPath, b.opts.CWD, b.opts.HomeDir)
	if err != nil {
		return err
	}
	b.configPath = configPath
	b.g = paths.NewGlobal(b.opts.HomeDir)
	b.l = paths.NewLocal(b.opts.CWD)
	if err := bootstrap.EnsureGlobal(b.g); err != nil {
		return err
	}
	uuid, err := bootstrap.EnsureProject(b.l, b.g, b.opts.CWD) // drop 3rd arg if removed in Task 2
	if err != nil {
		return err
	}
	b.uuid = uuid
	return nil
}
```

Remove the `proj paths.Project` field from `builder` and the `b.proj = paths.NewProject(...)` line.

- [ ] **Step 2: openStore + dbPath use canonical g.DB**

```go
func (b *builder) openStore() {
	if s, e := store.Open(b.g.DB); e == nil {
		b.st = s
		b.closers = append(b.closers, func() { _ = s.Close() })
	} else {
		b.log.Warn("open store failed — history unavailable", "error", e)
	}
}
```

In `BuildParts`, set `dbPath: b.g.DB` (was `b.proj.DB`).

- [ ] **Step 3: Environment section exposes project_uuid + the history command** — `environmentSection()`. Lead with `shell3 fts` (the supported read-only interface); keep `history_db` for advanced raw replay:

```go
func (p *Parts) environmentSection() string {
	if p.dbPath == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Environment\n")
	b.WriteString("Runtime paths for this session (read-only unless stated):\n")
	fmt.Fprintf(&b, "- project_uuid: %s\n", p.uuid)
	fmt.Fprintf(&b, "- search history: `shell3 fts \"<query>\" --project-id %s` (omit --project-id to search all projects; --page N to page; see the `history` skill)\n", p.uuid)
	fmt.Fprintf(&b, "- list projects: `shell3 list-projects` (--page N to page)\n")
	fmt.Fprintf(&b, "- history_db (advanced raw replay only): %s (open with `sqlite3 'file:%s?mode=ro'`)\n",
		p.dbPath, p.dbPath)
	return b.String()
}
```

- [ ] **Step 4: Build, verify**

Run: `go build ./... && go test ./internal/agentsetup/ -v`
Expected: PASS / clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/agentsetup/
git commit -m "feat(agentsetup): open canonical DB; expose project_uuid in Environment"
```

---

## Task 4: Session-start call sites pass project + workdir

**Files:**
- Modify: `pkg/shell3/shell3.go` (~405-414)
- Modify: `internal/chat/tools.go` (~126)
- Test: covered by Task 1 (signature) + Task 6 (e2e)

- [ ] **Step 1: pkg/shell3 session start** — at the StartSession(WithParent) site:

```go
		if opts.ParentSession != 0 {
			if id, err := cfg.Store.StartSessionWithParent(opts.ParentSession, cfg.ProjectRef, cfg.WorkDir); err == nil {
				storeID = id
			}
		} else {
			if id, err := cfg.Store.StartSession(cfg.ProjectRef, cfg.WorkDir); err == nil {
				storeID = id
			}
			// existing best-effort comment stays
		}
```

(Match the exact surrounding structure at `pkg/shell3/shell3.go:405`.)

- [ ] **Step 2: chat/tools.go /clear new session** — at `st.StartSession()` (~126), pass the config's project + workdir. Confirm the enclosing function has `cfg ToolConfig` (or equivalent) exposing `ProjectRef` and `WorkDir`; thread them:

```go
		newID, err := st.StartSession(cfg.ProjectRef, cfg.WorkDir)
```

If `ToolConfig` lacks those fields, add them from `chat.Config` (which already has `ProjectRef` and `WorkDir`) where `ToolConfig` is constructed.

- [ ] **Step 3: Build + full unit tests**

Run: `go build ./... && go test ./internal/... ./pkg/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add pkg/shell3/ internal/chat/
git commit -m "feat: thread project_uuid + workdir into session creation"
```

---

## Task 5: `shell3 fts` command — paginated, project-scoped history search

**Files:**
- Modify: `internal/store/store.go` (`HistorySearchExpr`)
- Create: `cmd/shell3/fts.go`
- Modify: `cmd/shell3/main.go` (register command)
- Test: `internal/store/store_test.go`, `cmd/shell3/fts_test.go`

- [ ] **Step 1: Write the failing store test** — `internal/store/store_test.go`:

```go
func TestHistorySearchExpr_ProjectScopedAndPaged(t *testing.T) {
	st := store.Open(filepath.Join(t.TempDir(), "s.db")) // adapt to existing helper
	// seed two projects (StartSession + AppendMessage drives the FTS insert)
	a, _ := st.StartSession("projA", "/a")
	b, _ := st.StartSession("projB", "/b")
	_ = st.AppendMessage(a, 0, llm.Message{Role: "user", Content: "alpha token here"})
	_ = st.AppendMessage(b, 0, llm.Message{Role: "user", Content: "alpha token here"})

	all, err := st.HistorySearchExpr("alpha", "", 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Hits) != 2 {
		t.Fatalf("all-projects search: got %d hits, want 2", len(all.Hits))
	}
	onlyA, _ := st.HistorySearchExpr("alpha", "projA", 20, 0)
	if len(onlyA.Hits) != 1 || onlyA.Hits[0].SessionID != a {
		t.Fatalf("project-scoped search: got %+v, want 1 hit in session %d", onlyA.Hits, a)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/store/ -run TestHistorySearchExpr_ProjectScopedAndPaged -v`
Expected: FAIL — `HistorySearchExpr` currently takes `(expr, limit)`.

- [ ] **Step 3: Extend `HistorySearchExpr`** to `(expr, projectUUID string, limit, offset int)`. Add the project filter and paging to the query:

```go
func (s *Store) HistorySearchExpr(expr, projectUUID string, limit, offset int) (HistorySearchResult, error) {
	if limit <= 0 {
		limit = 20
	}
	if expr == "" {
		return HistorySearchResult{}, nil
	}
	rows, err := s.db.Query(`
		SELECT rowid, CAST(session_id AS INTEGER), role, content, created_at,
			(SELECT COUNT(*) FROM history e
			 WHERE CAST(e.session_id AS INTEGER) = CAST(history.session_id AS INTEGER)
			   AND e.rowid < history.rowid) AS earlier
		FROM history
		WHERE history MATCH ?
		  AND (? = '' OR project_uuid = ?)
		ORDER BY rank
		LIMIT ? OFFSET ?
	`, expr, projectUUID, projectUUID, limit, offset)
	// ... rest of scan loop unchanged ...
}
```

Update all existing callers of `HistorySearchExpr` (search them: `grep -rn HistorySearchExpr internal/ pkg/ cmd/`) to pass `""` and `0` where they don't scope/page.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/store/ -v`
Expected: PASS.

- [ ] **Step 5: Add the `shell3 fts` command** — `cmd/shell3/fts.go`. It resolves the canonical DB from `HOME`, builds the FTS expr via the existing `BuildFTSExpr`, calls `HistorySearchExpr(expr, projectID, pageSize, page*pageSize)`, and prints `session | created_at | role | snippet` lines:

```go
//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
)

func newFTSCommand() *cobra.Command {
	var projectID string
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "fts [query]",
		Short: "Full-text search conversation history (read-only).",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			st, err := store.Open(paths.NewGlobal(home).DB)
			if err != nil {
				return err
			}
			defer st.Close()
			expr := store.BuildFTSExpr(strings.Join(args, " "))
			res, err := st.HistorySearchExpr(expr, projectID, pageSize, page*pageSize)
			if err != nil {
				return err
			}
			for _, h := range res.Hits {
				fmt.Printf("%d\t%s\t%s\t%s\n", h.SessionID,
					h.CreatedAt.Format("2006-01-02T15:04:05Z"), h.Role, snippet(h.Content))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&projectID, "project-id", "", "Scope to one project UUID (default: all projects).")
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index.")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Results per page.")
	return cmd
}

// snippet trims a hit's content to a single readable line.
func snippet(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 160 {
		s = s[:160] + "…"
	}
	return s
}
```

Add `"strings"` import. Register in `main.go`: `root.AddCommand(newFTSCommand())`.

- [ ] **Step 6: Build + smoke test**

Run: `go build ./... && go test ./cmd/shell3/ -v`
Expected: PASS / clean build.

- [ ] **Step 7: Commit**

```bash
git add internal/store/ cmd/shell3/fts.go cmd/shell3/main.go
git commit -m "feat(cli): shell3 fts — paginated, project-scoped history search"
```

---

## Task 6: `shell3 list-projects` command

**Files:**
- Modify: `internal/store/sessions.go` (add `ListProjects`)
- Create: `cmd/shell3/projects.go`
- Modify: `cmd/shell3/main.go` (register)
- Test: `internal/store/sessions_test.go`, `cmd/shell3/projects_test.go`

- [ ] **Step 1: Write the failing store test** — `internal/store/sessions_test.go`:

```go
func TestListProjects_DistinctWithLastActivity(t *testing.T) {
	st := store.Open(filepath.Join(t.TempDir(), "s.db")) // adapt to helper
	_, _ = st.StartSession("projA", "/a")
	_, _ = st.StartSession("projA", "/a") // second session, same project
	_, _ = st.StartSession("projB", "/b")
	ps, err := st.ListProjects(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 {
		t.Fatalf("got %d projects, want 2 (distinct)", len(ps))
	}
	// Most-recently-active first.
	if ps[0].UUID != "projB" && ps[0].UUID != "projA" {
		t.Fatalf("unexpected project ordering: %+v", ps)
	}
}
```

- [ ] **Step 2: Run, verify it fails**

Run: `go test ./internal/store/ -run TestListProjects_DistinctWithLastActivity -v`
Expected: FAIL — `ListProjects` undefined.

- [ ] **Step 3: Implement `ListProjects`** — `internal/store/sessions.go`:

```go
// ProjectInfo summarizes one project for `shell3 list-projects`.
type ProjectInfo struct {
	UUID         string
	Workdir      string
	SessionCount int
	LastActivity string // RFC3339, max(started_at) across the project's sessions
}

// ListProjects returns DISTINCT projects (by project_uuid) with their latest
// workdir, session count, and most-recent activity, newest-active first.
// Empty project_uuid rows (pre-tag/anonymous) are skipped.
func (s *Store) ListProjects(limit, offset int) ([]ProjectInfo, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`
		SELECT project_uuid,
		       (SELECT workdir FROM sessions x WHERE x.project_uuid = s.project_uuid
		        ORDER BY x.id DESC LIMIT 1) AS workdir,
		       COUNT(*) AS n,
		       MAX(started_at) AS last
		FROM sessions s
		WHERE project_uuid <> ''
		GROUP BY project_uuid
		ORDER BY last DESC
		LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ProjectInfo
	for rows.Next() {
		var p ProjectInfo
		if err := rows.Scan(&p.UUID, &p.Workdir, &p.SessionCount, &p.LastActivity); err != nil {
			return nil, fmt.Errorf("store: list projects: scan: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run, verify pass**

Run: `go test ./internal/store/ -run TestListProjects -v`
Expected: PASS.

- [ ] **Step 5: Add the `shell3 list-projects` command** — `cmd/shell3/projects.go`:

```go
//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/store"
)

func newListProjectsCommand() *cobra.Command {
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "list-projects",
		Short: "List projects (distinct) with workdir and last activity.",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			st, err := store.Open(paths.NewGlobal(home).DB)
			if err != nil {
				return err
			}
			defer st.Close()
			ps, err := st.ListProjects(pageSize, page*pageSize)
			if err != nil {
				return err
			}
			for _, p := range ps {
				fmt.Printf("%s\t%s\t%d sessions\tlast %s\n", p.UUID, p.Workdir, p.SessionCount, p.LastActivity)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index.")
	cmd.Flags().IntVar(&pageSize, "page-size", 20, "Projects per page.")
	return cmd
}
```

Register in `main.go`: `root.AddCommand(newListProjectsCommand())`.

- [ ] **Step 6: Build + test**

Run: `go build ./... && go test ./cmd/shell3/ -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/store/ cmd/shell3/projects.go cmd/shell3/main.go
git commit -m "feat(cli): shell3 list-projects — paginated project listing"
```

---

## Task 7: history skill doc — use `shell3 fts`, document project scoping

**Files:**
- Modify: `internal/scaffold/defaults/base/lib/skills/history.md`

- [ ] **Step 1: Rewrite the skill around `shell3 fts`.** Lead with the command as the primary interface:

```
## Searching history

Use the `shell3 fts` command (read-only by construction — you cannot mutate the DB):

    shell3 fts "JWT OR expiry" --project-id <project_uuid>   # this project
    shell3 fts "context window"                              # ALL projects
    shell3 fts "compact*" --page 1                           # next page

Your `project_uuid` is in the `## Environment` section. Omit `--project-id` to
search across every project. Output columns: session-id, timestamp, role, snippet.

## Listing projects

    shell3 list-projects            # distinct projects, newest-active first
    shell3 list-projects --page 1

FTS5 query tips: space-separated terms are AND; use `OR` for recall; quote a
phrase ("context window"); trailing `*` is a prefix match.
```

Keep a short "Advanced: raw replay" section noting `history_db` from the Environment for reading a full session in order via `sqlite3 'file:<history_db>?mode=ro'`, with the `project_uuid` column documented on both `sessions` and `history`. Remove the now-redundant hand-written FTS `MATCH` example (the command supersedes it).

- [ ] **Step 2: Verify scaffold test**

Run: `go test ./internal/scaffold/ -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/
git commit -m "docs(history skill): drive search via shell3 fts + list-projects"
```

---

## Task 8: e2e regression — divergent child CWD still reaches parent

**Files:**
- Modify: `test/delegation_e2e_test.go`

The working tree currently has a half-written `--cwd` draft (`TestDelegation_ChildResolvesParentViaCwdFlag`) referencing a flag we are NOT adding. Replace it with the CWD-independence proof, and update the project-DB helper to the canonical path.

- [ ] **Step 1: Replace the helper + draft test.** Change `findProjectDB` to point at the canonical DB:

```go
// canonicalDB returns the single per-home database path.
func canonicalDB(t *testing.T, homeDir string) string {
	t.Helper()
	db := filepath.Join(homeDir, ".shell3", "data", "shell3.db")
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("canonical db missing: %v", err)
	}
	return db
}
```

Update `TestDelegation_DormantParentRevived` to call `canonicalDB(t, homeDir)` instead of `findProjectDB`. Then **delete** `TestDelegation_ChildResolvesParentViaCwdFlag` and `findProjectDB`, and add:

```go
// TestDelegation_DivergentChildCwdStillReachesParent proves orchestration is
// CWD-independent: a child launched from a DIFFERENT directory than the parent
// (no --cwd, no shared pwd) still reports to and revives the parent, because
// both processes open the single canonical DB. This is the regression test for
// the silent black-hole bug where a divergent launch CWD routed the child's
// completion into a separate project DB.
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

	// Child B launched from otherDir (NOT workDir, NO --cwd). It must still reach
	// A through the single canonical DB and revive it.
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
```

- [ ] **Step 2: Run both e2e tests, verify fail-then-pass.** With Tasks 1-4 implemented they should pass; if running this task in isolation first, expect FAIL until the canonical DB exists.

Run: `go test ./test/ -run Delegation -v`
Expected: PASS for both `TestDelegation_DormantParentRevived` and `TestDelegation_DivergentChildCwdStillReachesParent`.

- [ ] **Step 3: Commit**

```bash
git add test/delegation_e2e_test.go
git commit -m "test(e2e): divergent child CWD still reaches parent via canonical DB"
```

---

## Task 9: Dead-code sweep + full verification

**Files:** repo-wide

- [ ] **Step 1: Find stragglers** referencing the deleted API:

Run:
```bash
grep -rn "NewProject\|paths.Project\|FindByCWD\|ReadMeta\|\.Projects\b\|proj\.DB\|meta\.json" \
  internal/ pkg/ cmd/ | grep -v _test.go
```
Expected: no matches. Fix any that remain (e.g. `cmd/shell3` boot output, telegram web server).

- [ ] **Step 2: Update any remaining tests** (`bootstrap_test.go`, `ref_test.go`, `paths` tests, `cli_e2e_test.go`) that glob `projects/*/shell3.db` or call deleted functions. Point them at `~/.shell3/data/shell3.db`.

- [ ] **Step 3: Full build + test + vet**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all PASS, pristine output.

- [ ] **Step 4: Manual smoke — divergent spawn is now safe.** Build, run a parent, spawn a subagent whose `bash_bg` workdir differs from the parent, confirm the ping arrives. (Or rely on Task 6's e2e as the automated equivalent.)

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: remove dead per-project-DB references; single canonical DB complete"
```

---

## Self-Review notes

- **Spec coverage:** canonical DB under `~/.shell3/data/` (Task 2/3), project_uuid namespacing (Task 1), per-project FTS (Task 1 step 5 + Task 5), `shell3 fts --project-id --page` with optional project (Task 5), `shell3 list-projects --page` (Task 6), history skill driven by the commands (Task 7), CWD-independent orchestration (Task 8 proof), clean break / delete old code (Task 2/9, no ALTER). ✓
- **Cross-project resume:** existing TUI `--resume <id>` and `shell3 run --resume <id>` now resolve any session because all front-ends (CLI + `~/.shell3/telegram/`) share `~/.shell3/data/shell3.db`. `shell3 list-projects` provides the discovery surface that the deleted `ls ~/.shell3/projects/` used to. No code change to resume itself — it inherits the single DB. ✓
- **Out of scope (flag for follow-up, do not silently skip):** the crash-strand finding (a parent stuck `status='live'` after `kill -9` whose `pid` is never checked) is *not* fixed here — it predates this change and remains. Worth a separate task: have `ClaimRevive`/`routeReport` reclaim a `'live'` row whose `pid` is dead. Also the swallowed `_ =` errors on `SetLiveness`/`AppendInbox` should log to `applog` so future transport failures aren't invisible.
- **Type consistency:** `StartSession(projectUUID, workdir)` and `StartSessionWithParent(parent, projectUUID, workdir)` used identically in Tasks 1 and 4. `HistorySearchExpr(expr, projectUUID, limit, offset)` defined in Task 5, no other signature elsewhere. `paths.Global.Data`/`Global.DB` defined in Task 2, consumed in Tasks 3/5/6. `ProjectInfo`/`ListProjects` defined in Task 6. `canonicalDB` helper defined and used in Task 8.
