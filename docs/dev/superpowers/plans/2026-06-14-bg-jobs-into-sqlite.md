# Background Jobs into SQLite + CLI-anchored agent prompts — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax. Follow TDD: write the failing test, watch it fail, implement, watch it pass. Gate every task on `make lint` (gofmt + go vet + golangci-lint) and `make test` (race + coverage). Commit per task.

**Goal:** Replace the per-workdir `.shell3/bg.json` background-job registry with a `jobs` table in the single canonical SQLite DB (`~/.shell3/data/shell3.db`), add a `shell3 jobs` CLI (`--config`, `--page`), and re-anchor the agent's prompts/affordances on the new CLIs (`fts`, `list-projects`, `list-sessions`, `jobs`) so it manages everything with plain bash + these commands.

**Why:** `bgjobs`' own doc admits the file registry only serializes writers *within one process* — cross-process writers sharing a workdir interleave and "last rename wins," silently dropping tracking entries. With multi-level delegation, multiple `shell3` processes share a workdir and each append to one `bg.json`. That is the same non-atomic cross-process data-loss class we just eliminated for orchestration (the wrong-DB report black-hole). The canonical DB (WAL + `busy_timeout`, stress-tested at 1024 concurrent writes, zero loss) makes these writes atomic, gives free pruning of dead jobs (the file is never pruned today), and unifies state behind one store.

**Architecture:**
- A `jobs` table in the canonical DB, keyed by the bg id, carrying pid/cmd/log/workdir/session/started_at.
- `bgjobs` stays decoupled from `store` via a small `Registry` interface it defines; a thin `internal/jobstore` adapter implements it over `*store.Store` (no import cycle: `bgjobs` imports neither `store` nor `jobstore`; `jobstore` imports both).
- Dead-pid pruning on list/add via a shared `internal/proc.Alive(pid)` (replaces the ad-hoc `pidAlive` in `pkg/shell3/transport.go`).
- `shell3 jobs` + `--config` on all helper CLIs via a shared DB resolver.
- **Clean break, no backward compat:** the migration is edited in place; existing `~/.shell3/data`, `~/.shell3/projects`, and stray `bg.json` files are DELETED at rollout (test data only — see [[git-stashes-disposable]] context: this is a dev project, state is disposable).

**Tech Stack:** Go, `modernc.org/sqlite` (WAL), cobra.

**Prereq branch:** builds on `feat/single-canonical-db` (single canonical DB + `fts`/`list-projects`/`list-sessions`). Branch this work as `feat/bg-jobs-sqlite` off that.

---

## DB resolution for the CLIs (design note — read before Task 3/4)

All front-ends today resolve the DB from `$HOME`: `store.Open(paths.NewGlobal(os.UserHomeDir()).DB)` → `~/.shell3/data/shell3.db`. The telegram bot is home-based too. So the DB is **always** `<global-root>/data/shell3.db` where the global root is `~/.shell3`.

`--config` is added for invocation parity (the agent already appends `--config $CFG` to `shell3 run`; the prompts should be uniform). Resolver rule (implement once, share across all four CLIs):

```
resolveDB(configFlag) :
  if configFlag != "":
     root = nearest ancestor dir of configFlag named ".shell3"   // ~/.shell3/shell3.lua and ~/.shell3/telegram/shell3.lua both → ~/.shell3
     if none: root = filepath.Dir(configFlag)
  else:
     root = filepath.Join(os.UserHomeDir(), ".shell3")
  return filepath.Join(root, "data", "shell3.db")
```

This yields `~/.shell3/data/shell3.db` for every real config (top-level and telegram-subfolder), matches the home-based default, and stays correct in tests (which set `$HOME`). Put it in `cmd/shell3` (e.g. `dbpath.go`) and reuse it.

---

## Task 0: Destructive reset (rollout — run once, no code)

**No backward compat.** Before/at rollout, wipe stale DBs and registries so the in-place migration starts clean.

- [ ] **Step 1: Delete canonical DB, legacy project dirs, and stray bg.json**

```bash
rm -rf ~/.shell3/data ~/.shell3/projects
# remove bg.json from any workdir you've run shell3 in (repo + test dirs):
find ~/CODE -path '*/.shell3/bg.json' -delete 2>/dev/null || true
rm -f /Users/weatherjean/CODE/AGENTS/shell3/.shell3/bg.json
```

This is safe — it is test/dev state only. A fresh `shell3` run recreates `~/.shell3/data/shell3.db` with the new schema (including `jobs`). Document this in the PR description as a required upgrade step.

---

## Task 1: `jobs` table + store API (with dead-pid pruning)

**Files:**
- Create: `internal/proc/proc.go` (shared `Alive(pid)`), `internal/proc/proc_test.go`
- Modify: `internal/store/store.go` (migrate), `internal/store/jobs.go` (new file for job methods + `Job` type), `internal/store/jobs_test.go`

- [ ] **Step 1: shared pid-liveness** — `internal/proc/proc.go`:

```go
//go:build unix

// Package proc holds tiny OS-process helpers shared across packages.
package proc

import "syscall"

// Alive reports whether pid names a running process. Signal 0 probes without
// delivering: nil (alive) or EPERM (alive, not ours) → alive; ESRCH → gone.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
```

Test `internal/proc/proc_test.go`: `Alive(os.Getpid())==true`, `Alive(2147483646)==false`, `Alive(0)==false`. Run `go test ./internal/proc/`.

- [ ] **Step 2: Failing store test** — `internal/store/jobs_test.go` (package `store`, mirror existing test style):

```go
func TestJobs_AddListPrunesDeadAndClear(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	live := os.Getpid()
	const dead = 2147483646
	must := func(err error) { if err != nil { t.Fatal(err) } }
	must(st.AddJob(Job{ID: "bg_live", PID: live, Cmd: "sleep 1", Workdir: "/w"}))
	must(st.AddJob(Job{ID: "bg_dead", PID: dead, Cmd: "echo hi", Workdir: "/w"}))
	must(st.AddJob(Job{ID: "bg_other", PID: live, Cmd: "x", Workdir: "/other"}))

	// List for /w prunes the dead entry and returns only the live one.
	jobs, err := st.ListJobs("/w", 50, 0)
	if err != nil { t.Fatal(err) }
	if len(jobs) != 1 || jobs[0].ID != "bg_live" {
		t.Fatalf("got %+v, want only bg_live", jobs)
	}
	// The dead row was pruned from the table.
	if n := jobCount(t, st); n != 2 { // bg_live + bg_other
		t.Fatalf("table has %d rows, want 2 (dead pruned)", n)
	}
	// Clear removes only /w's jobs, returns count cleared.
	n, err := st.ClearJobs("/w")
	if err != nil { t.Fatal(err) }
	if n != 1 { t.Fatalf("cleared %d, want 1", n) }
}

func jobCount(t *testing.T, st *Store) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow(`SELECT COUNT(*) FROM jobs`).Scan(&n); err != nil { t.Fatal(err) }
	return n
}
```

Run: `go test ./internal/store/ -run TestJobs_ -v` → FAIL (no `jobs` table / methods).

- [ ] **Step 3: migrate — add `jobs` table** in `internal/store/store.go` `migrate()` stmts:

```go
`CREATE TABLE IF NOT EXISTS jobs (
	id           TEXT PRIMARY KEY,
	pid          INTEGER NOT NULL,
	cmd          TEXT NOT NULL,
	log          TEXT NOT NULL DEFAULT '',
	workdir      TEXT NOT NULL DEFAULT '',
	session_id   INTEGER NOT NULL DEFAULT 0,
	project_uuid TEXT NOT NULL DEFAULT '',
	started_at   TEXT NOT NULL
)`,
`CREATE INDEX IF NOT EXISTS jobs_workdir ON jobs(workdir)`,
```

- [ ] **Step 4: job methods** — `internal/store/jobs.go`:

```go
package store

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/proc"
)

// Job is one tracked background process (the bash_bg / subagent / revive registry).
type Job struct {
	ID         string
	PID        int
	Cmd        string
	Log        string
	Workdir    string
	SessionID  int64
	StartedAt  time.Time
}

// AddJob records a spawned background process.
func (s *Store) AddJob(j Job) error {
	now := j.StartedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO jobs(id, pid, cmd, log, workdir, session_id, started_at)
		 VALUES(?,?,?,?,?,?,?)`,
		j.ID, j.PID, j.Cmd, j.Log, j.Workdir, j.SessionID, now.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store: add job %s: %w", j.ID, err)
	}
	return nil
}

// ListJobs returns tracked jobs for workdir (newest first), AFTER pruning any
// whose process has died — the registry is self-cleaning. limit<=0 → 50.
func (s *Store) ListJobs(workdir string, limit, offset int) ([]Job, error) {
	if err := s.pruneDeadJobs(workdir); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, pid, cmd, log, workdir, session_id, started_at
		 FROM jobs WHERE workdir = ? ORDER BY started_at DESC LIMIT ? OFFSET ?`,
		workdir, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		var j Job
		var started string
		if err := rows.Scan(&j.ID, &j.PID, &j.Cmd, &j.Log, &j.Workdir, &j.SessionID, &started); err != nil {
			return nil, fmt.Errorf("store: list jobs: scan: %w", err)
		}
		j.StartedAt = parseRFC3339(started)
		out = append(out, j)
	}
	return out, rows.Err()
}

// pruneDeadJobs deletes rows for workdir whose pid is no longer running.
func (s *Store) pruneDeadJobs(workdir string) error {
	rows, err := s.db.Query(`SELECT id, pid FROM jobs WHERE workdir = ?`, workdir)
	if err != nil {
		return fmt.Errorf("store: prune jobs scan: %w", err)
	}
	var deadIDs []string
	for rows.Next() {
		var id string
		var pid int
		if err := rows.Scan(&id, &pid); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: prune jobs scan row: %w", err)
		}
		if !proc.Alive(pid) {
			deadIDs = append(deadIDs, id)
		}
	}
	_ = rows.Close()
	for _, id := range deadIDs {
		if _, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id); err != nil {
			return fmt.Errorf("store: prune job %s: %w", id, err)
		}
	}
	return nil
}

// ClearJobs removes all jobs for workdir, returning the count removed. Used by
// KillAll after it has signalled the pids.
func (s *Store) ClearJobs(workdir string) (int, error) {
	res, err := s.db.Exec(`DELETE FROM jobs WHERE workdir = ?`, workdir)
	if err != nil {
		return 0, fmt.Errorf("store: clear jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
```

Note `gofmt` will align the `Job` struct fields. Run: `go test ./internal/store/ -run TestJobs_ -v` → PASS. Then `make lint` + `go test ./internal/store/`.

- [ ] **Step 5: Commit** — `git add internal/proc/ internal/store/ && git commit -m "feat(store): jobs table + self-pruning job registry API"`

---

## Task 2: `bgjobs` over the store registry; delete file registry + dead params

**Files:**
- Create: `internal/jobstore/jobstore.go`, `internal/jobstore/jobstore_test.go`
- Modify: `internal/bgjobs/bgjobs.go` (Start signature, Registry interface, KillAll; DELETE LoadRegistry/appendJob/clearRegistry/writeAtomic/registryPath), `internal/bgjobs/bgjobs_test.go`
- Modify callers: `internal/chat/handler_bash_bg.go`, `internal/chat/tools.go`, `pkg/shell3/transport.go`
- Modify: `internal/paths/paths.go` (drop `Local.BGJobs`)

- [ ] **Step 1: define the Registry interface in `bgjobs`** and drop the dead params. New `Start`:

```go
// Registry records and lists spawned background jobs. Implemented by
// internal/jobstore over the canonical store; kept as an interface here so
// bgjobs stays decoupled from store (no import cycle).
type Registry interface {
	Add(Job) error
	List(workdir string) ([]Job, error)
	Clear(workdir string) (int, error)
}

// Start spawns argv detached in workdir, records it in reg, and returns the Job.
// (The retired sink-file params sinkPath/notifyOnExit are removed.)
func Start(reg Registry, argv []string, display, workdir string, env []string) (Job, error) { ... }
```

Inside, replace `appendJob(workdir, job)` with `reg.Add(job)`. On `reg.Add` failure, keep the existing teardown (kill the group, remove log). **Delete** `LoadRegistry`, `appendJob`, `clearRegistry`, `writeAtomic`, `registryPath`, and the `fileLock`/`Registry` (the old struct) file-registry code. Rewrite `KillAll`:

```go
// KillAll signals every tracked job for workdir (whole process group) and clears
// the registry. Returns the number of live jobs signalled.
func KillAll(reg Registry, workdir string) (int, error) {
	jobs, err := reg.List(workdir)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, j := range jobs {
		if j.PID <= 0 || syscall.Kill(j.PID, 0) != nil {
			continue
		}
		if syscall.Kill(-j.PID, syscall.SIGKILL) == nil {
			killed++
		}
	}
	if _, err := reg.Clear(workdir); err != nil {
		return killed, fmt.Errorf("bgjobs: clear registry: %w", err)
	}
	return killed, nil
}
```

Keep `bgjobs.Job` (the existing struct) as the wire type. Update `bgjobs_test.go` (and delete `bgjobs_argv_test.go` only if it tests removed file-registry behavior; otherwise update). Drop now-unused imports (`encoding/json`, `path/filepath`, `sync`, etc. as appropriate).

- [ ] **Step 2: the adapter** — `internal/jobstore/jobstore.go` (imports BOTH bgjobs + store; neither imports it):

```go
package jobstore

import (
	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/store"
)

// Store adapts *store.Store to bgjobs.Registry, translating bgjobs.Job <-> store.Job.
type Store struct{ st *store.Store }

func New(st *store.Store) *Store { return &Store{st: st} }

func (a *Store) Add(j bgjobs.Job) error {
	return a.st.AddJob(store.Job{
		ID: j.ID, PID: j.PID, Cmd: j.Cmd, Log: j.Log, Workdir: j.Workdir, StartedAt: j.StartedAt,
	})
}

func (a *Store) List(workdir string) ([]bgjobs.Job, error) {
	rows, err := a.st.ListJobs(workdir, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]bgjobs.Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, bgjobs.Job{
			ID: r.ID, PID: r.PID, Cmd: r.Cmd, Log: r.Log, Workdir: r.Workdir, StartedAt: r.StartedAt,
		})
	}
	return out, nil
}

func (a *Store) Clear(workdir string) (int, error) { return a.st.ClearJobs(workdir) }
```

Add a `jobstore_test.go` round-tripping Add→List→Clear against a real `store.Open(":memory:")`.

- [ ] **Step 3: update callers** to pass `jobstore.New(cfg.Store)` (guard nil store):
  - `internal/chat/handler_bash_bg.go:55` — `bgjobs.Start(jobstore.New(cfg.Store), argv, p.Command, wd, nil)`. Drop the `notifyOnExit` plumbing entirely (parse of `notify_on_exit` arg → remove; the field/param are gone). If `cfg.Store == nil`, return a clear error (bg jobs require the store now).
  - `internal/chat/tools.go:75` — same wrapping for the custom command-tool spawn.
  - `pkg/shell3/transport.go:169` (`spawnRevive`) — `bgjobs.Start(jobstore.New(st), argv, "revive session "+..., s.cfg.WorkDir, nil)`.
  - `internal/telegram/commands.go:71` — `bgjobs.KillAll(jobstore.New(b.store), b.workDir)` (thread the store into the bot if not already present).

- [ ] **Step 4: drop `paths.Local.BGJobs`** in `internal/paths/paths.go` and its doc line. Update any reference.

- [ ] **Step 5: validate** — `go build ./...`, `go test ./internal/bgjobs/ ./internal/jobstore/ ./internal/chat/ ./pkg/shell3/ ./internal/telegram/`, `make lint`.

- [ ] **Step 6: Commit** — `git commit -m "refactor(bgjobs): back the job registry with the canonical store; drop bg.json file + dead sink params"`

---

## Task 3: `shell3 jobs` command (`--config`, `--page`)

**Files:**
- Create: `cmd/shell3/dbpath.go` (shared `--config` DB resolver), `cmd/shell3/jobs.go`, `cmd/shell3/jobs_test.go`
- Modify: `cmd/shell3/main.go` (register)

- [ ] **Step 1: shared resolver** — `cmd/shell3/dbpath.go` implementing the rule in the design note:

```go
//go:build unix

package main

import (
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/paths"
)

// canonicalDBPath resolves the single canonical DB. With --config it anchors to
// the nearest ".shell3" ancestor of the config (so ~/.shell3/shell3.lua and
// ~/.shell3/telegram/shell3.lua both map to ~/.shell3/data); otherwise it uses
// $HOME, matching the runtime.
func canonicalDBPath(configFlag string) (string, error) {
	if configFlag != "" {
		if root := nearestShell3Dir(configFlag); root != "" {
			return filepath.Join(root, "data", "shell3.db"), nil
		}
		return filepath.Join(filepath.Dir(configFlag), "data", "shell3.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return paths.NewGlobal(home).DB, nil
}

func nearestShell3Dir(p string) string {
	for d := filepath.Dir(p); ; {
		if filepath.Base(d) == ".shell3" {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return ""
		}
		d = parent
	}
}
```

- [ ] **Step 2: failing command test** — `cmd/shell3/jobs_test.go` (mirror `sessions_test.go`): seed a DB at `<tmpHome>/.shell3/data/shell3.db` with `AddJob` (use a LIVE pid = `os.Getpid()` so it survives pruning; the workdir must match what the command lists). Assert `--flags present` and that `jobs` lists the seeded id. Run → FAIL (no command).

- [ ] **Step 3: implement** — `cmd/shell3/jobs.go` (mirror `sessions.go`):

```go
//go:build unix

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/store"
)

func newJobsCommand() *cobra.Command {
	var configPath, workdir string
	var page, pageSize int
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List tracked background jobs for a workdir (read-only; dead jobs auto-pruned).",
		RunE: func(cmd *cobra.Command, args []string) error {
			dbPath, err := canonicalDBPath(configPath)
			if err != nil {
				return fmt.Errorf("jobs: resolve db: %w", err)
			}
			wd := workdir
			if wd == "" {
				if wd, err = os.Getwd(); err != nil {
					return fmt.Errorf("jobs: cwd: %w", err)
				}
			}
			st, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("jobs: open store: %w", err)
			}
			defer func() { _ = st.Close() }()
			jobs, err := st.ListJobs(wd, pageSize, page*pageSize)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, j := range jobs {
				fmt.Fprintf(out, "%s\tpid:%d\t%s\t%s\n", j.ID, j.PID, j.Log, j.Cmd)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to shell3.lua (anchors the canonical DB; default: ~/.shell3).")
	cmd.Flags().StringVar(&workdir, "workdir", "", "Workdir whose jobs to list (default: current directory).")
	cmd.Flags().IntVar(&page, "page", 0, "Zero-based page index.")
	cmd.Flags().IntVar(&pageSize, "page-size", 50, "Jobs per page.")
	return cmd
}
```

Register in `main.go`: `root.AddCommand(newJobsCommand())`. Run the test → PASS.

- [ ] **Step 4: validate + commit** — `make lint`, `go test ./cmd/shell3/`. `git commit -m "feat(cli): shell3 jobs — list tracked background jobs (--config, --page)"`

---

## Task 4: retrofit `--config` on `fts` / `list-projects` / `list-sessions`

**Files:** `cmd/shell3/fts.go`, `cmd/shell3/projects.go`, `cmd/shell3/sessions.go` + their tests.

- [ ] **Step 1:** each command: add `--config`/`-c` flag and replace the `os.UserHomeDir()`+`paths.NewGlobal(home).DB` block with `dbPath, err := canonicalDBPath(configPath)`. Keep their existing `--page`/`--project-id` flags. Update each `*_test.go` to still pass (they set `$HOME`, so `canonicalDBPath("")` resolves the same path — no test change expected; verify).

- [ ] **Step 2: validate + commit** — `make lint`, `go test ./cmd/shell3/`. `git commit -m "feat(cli): accept --config on fts/list-projects/list-sessions (uniform DB resolution)"`

---

## Task 5: re-anchor agent prompts/affordances on the CLIs

**Files:** `internal/luacfg/tooldefs.go`, `internal/chat/handler_bash_bg.go`, `internal/agentsetup/agentsetup.go`, `internal/scaffold/defaults/base/lib/skills/history.md`, the scaffolded default config/prompts under `internal/scaffold/defaults/` (grep for them), `internal/bootstrap/bootstrap.go` (gitignore), `pkg/shell3/delegation.go` (delegation prompt if it references bg.json), `CLAUDE.md`.

The agent currently learns to manage bg jobs via `cat .shell3/bg.json`. Re-anchor on `shell3 jobs`.

- [ ] **Step 1: `bash_bg` affordances** — in `internal/luacfg/tooldefs.go` replace `` `cat .shell3/bg.json` to list `` with `` `shell3 jobs` to list ``. In `internal/chat/handler_bash_bg.go` change the returned "manage with bash" block: `list:   shell3 jobs` (drop the `cat .shell3/bg.json` line and its `%s/.shell3/bg.json` arg). Keep status/output/kill (`kill -0`, `tail`, `kill`).

- [ ] **Step 2: Environment section** — `internal/agentsetup/agentsetup.go` `environmentSection`: add a `- list jobs: ` `shell3 jobs --config <cfg>` ` (--page N)` line alongside the existing fts/list-projects/list-sessions lines, so all four CLIs are advertised with `--config`. (The config path is session-resolvable; if not readily available there, reference the command without the value and note `--config` is the same path used for delegation.)

- [ ] **Step 3: scaffold default agent prompts** — grep the scaffold defaults for any prose telling the agent to read history/jobs (`grep -rn "bg.json\|sqlite3\|history\|background job" internal/scaffold/defaults/`). Rewrite those to point at the four CLIs (`shell3 fts`, `shell3 list-projects`, `shell3 list-sessions`, `shell3 jobs`, each `--config $CFG`). Keep the bash-first voice.

- [ ] **Step 4: history skill** — `internal/scaffold/defaults/base/lib/skills/history.md`: it already covers fts/list-projects/list-sessions; add a short "Background jobs" section pointing at `shell3 jobs`.

- [ ] **Step 5: gitignore** — `internal/bootstrap/bootstrap.go`: drop `bg.json` from the project `.gitignore` lines (no such file anymore); keep `.ref` and `proxy-*.log`. Leave existing user gitignores alone.

- [ ] **Step 6: CLAUDE.md** — update the `internal/bgjobs/` line ("background job tracking (.shell3/bg.json)") to describe the store-backed registry; update any "history is a read-only SQLite query" prose to mention the CLIs are the agent's entry points.

- [ ] **Step 7: validate + commit** — `make lint`, `go test ./...`. `git commit -m "docs/prompts: anchor agent on shell3 fts/list-projects/list-sessions/jobs; retire bg.json affordance"`

---

## Task 6: dead/old-code cleanup sweep

- [ ] **Step 1: straggler grep** — `grep -rn "bg.json\|BGJobs\|notifyOnExit\|sinkPath\|appendJob\|LoadRegistry\|clearRegistry\|writeAtomic\|fileLock" internal/ pkg/ cmd/ | grep -v _test` → expect ZERO (outside historical comments). Remove anything left. Confirm `internal/bgjobs` no longer imports `encoding/json`/`path/filepath`/`sync` if unused.
- [ ] **Step 2: unused-symbol check** — `go vet ./...` and a quick scan for now-unused helpers/fields (e.g. anything that only served the file registry). Remove.
- [ ] **Step 3: full gates** — `make lint` (0 issues) and `make test` (race + coverage, all green).
- [ ] **Step 4: Commit** — `git commit -m "chore: remove dead bg.json file-registry code"`

---

## Task 7: end-to-end verification

- [ ] **Step 1: bg job lifecycle e2e** (`test/`): build the binary; run a `bash_bg` job that sleeps, assert it appears in `shell3 jobs`; kill it; assert `shell3 jobs` prunes it on next list. Mirror `delegation_e2e_test.go` harness (short `/tmp` workdir, `HOME`-isolated tmp).
- [ ] **Step 2: KillAll via store** — assert `bgjobs.KillAll(jobstore.New(st), workdir)` kills tracked pids and clears the rows (unit or e2e).
- [ ] **Step 3: subagent still tracked + killable** — a delegated subagent spawn appears in the `jobs` table (so `/stop` can kill it). Verify the existing delegation e2e still passes.
- [ ] **Step 4: Commit** — `git commit -m "test(e2e): bg job lifecycle + prune + KillAll over the store registry"`

---

## Self-Review checklist
- Clean break / destructive reset (Task 0) — covered; documented as a rollout step.
- Cross-process atomicity (the why) — store-backed writes (Task 1/2).
- Dead-pid pruning (the wart) — `pruneDeadJobs` (Task 1).
- `shell3 jobs` with `--config` + `--page` — Task 3.
- `--config` on all four CLIs — Tasks 3/4.
- Prompts anchored on the CLIs — Task 5.
- Dead/old code removed — Task 6.
- `bgjobs` decoupled from `store` (interface + adapter, no cycle) — Task 2.
- Type consistency: `store.Job` ↔ `bgjobs.Job` bridged in `internal/jobstore`; `proc.Alive` shared; `canonicalDBPath` shared across CLIs.
