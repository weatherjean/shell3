# Project Secrets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace plaintext `.shell3/.env` with an XOR-obfuscated project secrets store managed by `shell3 secrets {set,list,remove}`. Drop the `destroy` subcommand.

**Architecture:** New `internal/secrets` package mirroring `internal/config/credstore.go`, reusing `config.Wrap`/`Unwrap` for the on-disk format. New `cmd/shell3/secrets.go` cobra subcommand. `run.go` swaps `usertools.LoadDotEnv` + `os.Environ` overlay for `secrets.Load(cwd).All()`. Scaffold drops `.env`/`.env.example`, adds `secrets.shell3` to `.gitignore`. `usertools.Validate` error message updates to point at `shell3 secrets set` instead of `.shell3/.env`.

**Tech Stack:** Go, cobra, yaml.v3, existing `internal/config` obfuscation helpers.

**Spec:** `docs/superpowers/specs/2026-04-27-project-secrets-design.md`

---

## File Structure

**New:**
- `internal/secrets/store.go` — `Store` type with `Load`, `Set`, `Remove`, `Get`, `List`, `All`.
- `internal/secrets/store_test.go` — round-trip + persistence tests.
- `cmd/shell3/secrets.go` — `shell3 secrets` cobra command tree.

**Modified:**
- `cmd/shell3/main.go` — register `newSecretsCommand`, drop `newDestroyCommand`.
- `cmd/shell3/run.go` — replace dotenv block (lines 101-116) with secrets store load.
- `internal/scaffold/scaffold.go` — drop `.env.example`, drop `.env` reference from `defaultGitignore`, add `secrets.shell3` line.
- `internal/scaffold/scaffold_test.go` — update assertions: `TestInit_CreatesToolsDirAndExample` no longer expects `.env.example`; `TestInit_GitignoreContainsDotEnv` becomes `TestInit_GitignoreContainsSecretsShell3`.
- `internal/usertools/usertools.go` — update line 124 error message ("not set in .shell3/.env or environment" → "not set; run `shell3 secrets set --key NAME --secret VALUE`").
- `internal/usertools/usertools.go` — also update line 52 doc comment ("availableSecrets is the set of keys present in .env+OS env" → "...present in the project secrets store").
- `README.md` — add "Removing a project's shell3 data" section.

**Deleted:**
- `cmd/shell3/destroy.go`
- `internal/usertools/dotenv.go`
- `internal/usertools/dotenv_test.go`

---

### Task 1: Skeleton of `internal/secrets/store.go`

**Files:**
- Create: `internal/secrets/store.go`

- [ ] **Step 1: Write the failing test (load empty)**

Create `internal/secrets/store_test.go`:

```go
package secrets_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/secrets"
)

func TestLoad_EmptyWhenNoFile(t *testing.T) {
	dir := t.TempDir()
	if err := mkProjectDir(t, dir); err != nil {
		t.Fatal(err)
	}
	s, err := secrets.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.List(); len(got) != 0 {
		t.Fatalf("expected empty list, got %v", got)
	}
}

func mkProjectDir(t *testing.T, dir string) error {
	t.Helper()
	return os.MkdirAll(filepath.Join(dir, ".shell3"), 0700)
}
```

Add the missing `os` import inline when running.

- [ ] **Step 2: Run test (expect compile failure)**

Run: `go test ./internal/secrets/...`
Expected: FAIL — package `internal/secrets` does not exist.

- [ ] **Step 3: Implement minimal store**

Create `internal/secrets/store.go`:

```go
// Package secrets manages project-scoped tool secrets stored under
// <projectDir>/.shell3/secrets.shell3. The on-disk file is wrapped with
// the same XOR obfuscation as the credential store; this defends
// against accidental disclosure (e.g. an LLM tool reading the file
// verbatim), not against a determined attacker.
package secrets

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/config"
)

type secretsFile struct {
	Version int               `yaml:"version"`
	Secrets map[string]string `yaml:"secrets"`
}

// Store is the project secrets store. Keys are environment-variable
// style names; values are raw secret strings.
type Store struct {
	projectDir string

	mu   sync.Mutex
	data secretsFile
}

// Load reads <projectDir>/.shell3/secrets.shell3 if present. The
// .shell3/ directory must exist (project must be inited); otherwise
// Load returns an error directing the user to run `shell3 init`.
func Load(projectDir string) (*Store, error) {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	if _, err := os.Stat(shell3Dir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("secrets: no .shell3/ in %s — run `shell3 init`", projectDir)
		}
		return nil, fmt.Errorf("secrets: stat %s: %w", shell3Dir, err)
	}

	s := &Store{
		projectDir: projectDir,
		data:       secretsFile{Version: 1, Secrets: map[string]string{}},
	}
	blob, err := os.ReadFile(secretsPath(projectDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, fmt.Errorf("secrets: read: %w", err)
	}
	plain, err := config.Unwrap(blob)
	if err != nil {
		return nil, fmt.Errorf("secrets: unwrap: %w", err)
	}
	if err := yaml.Unmarshal(plain, &s.data); err != nil {
		return nil, fmt.Errorf("secrets: parse: %w", err)
	}
	if s.data.Secrets == nil {
		s.data.Secrets = map[string]string{}
	}
	return s, nil
}

func secretsPath(projectDir string) string {
	return filepath.Join(projectDir, ".shell3", "secrets.shell3")
}

// List returns secret names sorted alphabetically. Values are never
// returned by this method.
func (s *Store) List() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.data.Secrets))
	for k := range s.data.Secrets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// All returns a copy of every key/value pair. Used at runtime to seed
// tool secret availability.
func (s *Store) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.data.Secrets))
	for k, v := range s.data.Secrets {
		out[k] = v
	}
	return out
}

// Get returns the raw value for a key, if present.
func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Secrets[key]
	return v, ok
}

// Set writes (or overwrites) one secret and persists.
func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Secrets[key] = value
	return s.saveLocked()
}

// Remove deletes a secret. No-op if absent.
func (s *Store) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.data.Secrets[key]; !ok {
		return nil
	}
	delete(s.data.Secrets, key)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	dir := filepath.Join(s.projectDir, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("secrets: mkdir: %w", err)
	}
	plain, err := yaml.Marshal(s.data)
	if err != nil {
		return fmt.Errorf("secrets: marshal: %w", err)
	}
	wrapped := config.Wrap(plain)
	path := secretsPath(s.projectDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, wrapped, 0600); err != nil {
		return fmt.Errorf("secrets: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("secrets: rename: %w", err)
	}
	return nil
}
```

Add `"os"` to the test file imports:

```go
import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/secrets"
)
```

- [ ] **Step 4: Run test**

Run: `go test ./internal/secrets/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/store.go internal/secrets/store_test.go
git commit -m "feat(secrets): scaffold project secrets store"
```

---

### Task 2: Set + Get round-trip test

**Files:**
- Modify: `internal/secrets/store_test.go`

- [ ] **Step 1: Add round-trip test**

Append to `internal/secrets/store_test.go`:

```go
func TestSetGetRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if err := mkProjectDir(t, dir); err != nil {
		t.Fatal(err)
	}

	s, err := secrets.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("BRAVE_API_KEY", "abc123xyz"); err != nil {
		t.Fatal(err)
	}

	s2, err := secrets.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := s2.Get("BRAVE_API_KEY")
	if !ok || v != "abc123xyz" {
		t.Fatalf("Get: got (%q,%v), want (%q,true)", v, ok, "abc123xyz")
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./internal/secrets/... -run TestSetGetRoundTrip`
Expected: PASS (already implemented in Task 1).

- [ ] **Step 3: Verify on-disk file is wrapped**

Add another test:

```go
func TestSet_FileIsWrapped(t *testing.T) {
	dir := t.TempDir()
	if err := mkProjectDir(t, dir); err != nil {
		t.Fatal(err)
	}
	s, err := secrets.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Set("BRAVE_API_KEY", "shouldnotappear"); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(filepath.Join(dir, ".shell3", "secrets.shell3"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("shouldnotappear")) {
		t.Fatal("secrets file contains plaintext secret")
	}
}
```

Add `"bytes"` to test imports.

- [ ] **Step 4: Run test**

Run: `go test ./internal/secrets/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/store_test.go
git commit -m "test(secrets): round-trip + on-disk wrapping"
```

---

### Task 3: Remove + List tests

**Files:**
- Modify: `internal/secrets/store_test.go`

- [ ] **Step 1: Add tests**

Append:

```go
func TestRemove(t *testing.T) {
	dir := t.TempDir()
	if err := mkProjectDir(t, dir); err != nil {
		t.Fatal(err)
	}
	s, _ := secrets.Load(dir)
	s.Set("A", "1")
	s.Set("B", "2")
	if err := s.Remove("A"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("A"); ok {
		t.Fatal("A still present after Remove")
	}
	// Remove of missing key is a no-op.
	if err := s.Remove("MISSING"); err != nil {
		t.Fatalf("Remove of missing: %v", err)
	}
}

func TestList_Sorted(t *testing.T) {
	dir := t.TempDir()
	if err := mkProjectDir(t, dir); err != nil {
		t.Fatal(err)
	}
	s, _ := secrets.Load(dir)
	s.Set("ZED", "z")
	s.Set("ALPHA", "a")
	s.Set("MID", "m")
	got := s.List()
	want := []string{"ALPHA", "MID", "ZED"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List: got %v, want %v", got, want)
	}
}
```

Add `"reflect"` to test imports.

- [ ] **Step 2: Run tests**

Run: `go test ./internal/secrets/...`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/secrets/store_test.go
git commit -m "test(secrets): remove + list ordering"
```

---

### Task 4: Refuses outside inited project

**Files:**
- Modify: `internal/secrets/store_test.go`

- [ ] **Step 1: Test that Load fails without `.shell3/`**

Append:

```go
func TestLoad_RequiresInitedProject(t *testing.T) {
	dir := t.TempDir()
	_, err := secrets.Load(dir)
	if err == nil {
		t.Fatal("expected error when .shell3/ missing")
	}
	if !strings.Contains(err.Error(), "shell3 init") {
		t.Fatalf("error %q should mention `shell3 init`", err.Error())
	}
}
```

Add `"strings"` to test imports.

- [ ] **Step 2: Run test**

Run: `go test ./internal/secrets/...`
Expected: PASS (already implemented in Task 1's `Load`).

- [ ] **Step 3: Commit**

```bash
git add internal/secrets/store_test.go
git commit -m "test(secrets): Load refuses uninited project"
```

---

### Task 5: `shell3 secrets` CLI

**Files:**
- Create: `cmd/shell3/secrets.go`
- Modify: `cmd/shell3/main.go`

- [ ] **Step 1: Create the command tree**

Create `cmd/shell3/secrets.go`:

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/secrets"
)

func newSecretsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage project tool secrets",
		Long: `Manage project tool secrets.

Secrets live in the obfuscated store at .shell3/secrets.shell3 (project-
scoped). They are exposed only to user tools that declare the matching
name in their tool YAML's "secrets:" field.

Operations:
  shell3 secrets set --key NAME --secret VALUE   write or overwrite one secret
  shell3 secrets list                             list names with last 3 chars masked
  shell3 secrets remove --key NAME                delete one secret`,
	}
	cmd.AddCommand(newSecretsSetCommand())
	cmd.AddCommand(newSecretsListCommand())
	cmd.AddCommand(newSecretsRemoveCommand())
	return cmd
}

func newSecretsSetCommand() *cobra.Command {
	var key, secret string
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Set or overwrite a secret",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return fmt.Errorf("--key is required")
			}
			if secret == "" {
				return fmt.Errorf("--secret is required")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			s, err := secrets.Load(cwd)
			if err != nil {
				return err
			}
			if err := s.Set(key, secret); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Set %s\n", key)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Secret name (e.g. BRAVE_API_KEY)")
	cmd.Flags().StringVar(&secret, "secret", "", "Secret value")
	return cmd
}

func newSecretsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured secret names (values masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			s, err := secrets.Load(cwd)
			if err != nil {
				return err
			}
			return runSecretsList(s, cmd.OutOrStdout())
		},
	}
}

func newSecretsRemoveCommand() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "remove",
		Short: "Remove a secret",
		RunE: func(cmd *cobra.Command, args []string) error {
			if key == "" {
				return fmt.Errorf("--key is required")
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			s, err := secrets.Load(cwd)
			if err != nil {
				return err
			}
			if err := s.Remove(key); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed %s\n", key)
			return nil
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "Secret name to remove")
	return cmd
}

func runSecretsList(s *secrets.Store, out io.Writer) error {
	names := s.List()
	if len(names) == 0 {
		fmt.Fprintln(out, "No secrets configured. Run: shell3 secrets set --key NAME --secret VALUE")
		return nil
	}
	all := s.All()
	fmt.Fprintf(out, "%-32s  %s\n", "NAME", "VALUE")
	for _, name := range names {
		fmt.Fprintf(out, "%-32s  %s\n", name, maskSecret(all[name]))
	}
	return nil
}

// maskSecret returns the value with its last 3 characters replaced by
// asterisks. Values shorter than 3 characters are entirely masked.
func maskSecret(v string) string {
	if len(v) <= 3 {
		return "***"
	}
	return v[:len(v)-3] + "***"
}
```

- [ ] **Step 2: Register on root cobra**

Modify `cmd/shell3/main.go` — replace:

```go
	root.AddCommand(newInitCommand())
	root.AddCommand(newAuthCommand())
	root.AddCommand(newDocsCommand())
	root.AddCommand(newDestroyCommand())
	root.AddCommand(newWidgetCommand())
```

with:

```go
	root.AddCommand(newInitCommand())
	root.AddCommand(newAuthCommand())
	root.AddCommand(newSecretsCommand())
	root.AddCommand(newDocsCommand())
	root.AddCommand(newWidgetCommand())
```

(Note: `newDestroyCommand` registration is removed; the file is deleted in Task 6.)

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: succeeds. (At this point `cmd/shell3/destroy.go` still exists but is no longer registered. Compile is fine since cobra doesn't require registration to compile the function.)

- [ ] **Step 4: Smoke test**

```bash
go build -o /tmp/shell3 ./cmd/shell3
mkdir -p /tmp/proj/.shell3
cd /tmp/proj
/tmp/shell3 secrets list                                   # "No secrets configured."
/tmp/shell3 secrets set --key BRAVE_API_KEY --secret abcdefxyz
/tmp/shell3 secrets list                                   # NAME            abcdef***
/tmp/shell3 secrets remove --key BRAVE_API_KEY
/tmp/shell3 secrets list                                   # "No secrets configured."
cd - && rm -rf /tmp/proj
```

Expected: each command prints the expected output above.

- [ ] **Step 5: Commit**

```bash
git add cmd/shell3/secrets.go cmd/shell3/main.go
git commit -m "feat(cli): shell3 secrets {set,list,remove}"
```

---

### Task 6: Drop `destroy` command

**Files:**
- Delete: `cmd/shell3/destroy.go`

- [ ] **Step 1: Delete the file**

```bash
rm cmd/shell3/destroy.go
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: succeeds. (Already removed from main.go in Task 5.)

- [ ] **Step 3: Confirm command no longer listed**

```bash
go build -o /tmp/shell3 ./cmd/shell3
/tmp/shell3 --help | grep -i destroy
```
Expected: no output (grep exits 1).

- [ ] **Step 4: Commit**

```bash
git add -A cmd/shell3/destroy.go
git commit -m "feat(cli): drop shell3 destroy — use rm -rf .shell3"
```

---

### Task 7: Wire secrets store into runtime

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Replace dotenv block with secrets load**

In `cmd/shell3/run.go`, replace lines 101-116 (the `envPath := ...` through `for _, kv := range os.Environ() { ... }` block ending with the close of the for loop) with:

```go
	secStore, err := secrets.Load(cwd)
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}
	secretsMap := secStore.All()
	available := map[string]struct{}{}
	for k := range secretsMap {
		available[k] = struct{}{}
	}
```

- [ ] **Step 2: Update imports**

Replace `"github.com/weatherjean/shell3/internal/usertools"` is kept (still used for `LoadAll`). Add `"github.com/weatherjean/shell3/internal/secrets"`. Remove the `"strings"` import only if it's no longer used elsewhere in the file — check by searching `strings.` after edit. (It is used by `strings.Join(args, " ")` near the top, so keep it.)

- [ ] **Step 3: Update the variable name passed downstream**

Search `run.go` for `secrets:` field on `chat.Config`. Currently:

```go
		Secrets:       secrets,
```

Rename the local `secrets` map (was assembled from dotEnv+os.Environ) to `secretsMap` everywhere in `run.go` to avoid collision with the new `secrets` package import. Update the `chat.Config` field assignment to:

```go
		Secrets:       secretsMap,
```

- [ ] **Step 4: Build**

Run: `go build ./...`
Expected: succeeds.

- [ ] **Step 5: Run all tests**

Run: `go test ./...`
Expected: PASS for `internal/secrets`, `internal/usertools`, `internal/scaffold` will fail (Task 8 fixes it). Verify that secrets/usertools tests pass; expect scaffold tests to still pass *until* Task 8 changes scaffold behavior. Run: `go test ./internal/secrets/... ./cmd/...`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(run): load tool secrets from secrets store, drop .env"
```

---

### Task 8: Update scaffold (drop `.env`, gitignore `secrets.shell3`)

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Update `defaultGitignore`**

In `internal/scaffold/scaffold.go`, replace:

```go
const defaultGitignore = `# shell3 runtime files — do not commit
shell3.db
memory.db
history.md
last_error.json
.env
`
```

with:

```go
const defaultGitignore = `# shell3 runtime files — do not commit
shell3.db
memory.db
history.md
last_error.json
secrets.shell3
`
```

- [ ] **Step 2: Update `braveSearchTool` description**

Replace the description line:

```go
description: Web search via the Brave Search API. Returns top results as JSON. Set enabled to true after putting BRAVE_API_KEY in .shell3/.env.
```

with:

```go
description: Web search via the Brave Search API. Returns top results as JSON. Set enabled to true after running `shell3 secrets set --key BRAVE_API_KEY --secret <token>`.
```

- [ ] **Step 3: Drop `.env.example` constant + write**

Delete the entire `envExample` const block. In the `files` map inside `initShell3Dir`, remove the line:

```go
		filepath.Join(shell3Dir, ".env.example"):              envExample,
```

- [ ] **Step 4: Update scaffold tests**

Edit `internal/scaffold/scaffold_test.go`:

In `TestInit_CreatesToolsDirAndExample`, replace:

```go
	for _, p := range []string{
		".shell3/tools",
		".shell3/tools/brave_search.yaml",
		".shell3/.env.example",
	} {
```

with:

```go
	for _, p := range []string{
		".shell3/tools",
		".shell3/tools/brave_search.yaml",
	} {
```

Replace `TestInit_GitignoreContainsDotEnv` entirely with:

```go
func TestInit_GitignoreContainsSecretsShell3(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)
	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".shell3", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "secrets.shell3") {
		t.Errorf("gitignore missing secrets.shell3 line:\n%s", data)
	}
}
```

- [ ] **Step 5: Run scaffold tests**

Run: `go test ./internal/scaffold/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go
git commit -m "feat(scaffold): drop .env scaffolding, gitignore secrets.shell3"
```

---

### Task 9: Delete `internal/usertools/dotenv.go`

**Files:**
- Delete: `internal/usertools/dotenv.go`
- Delete: `internal/usertools/dotenv_test.go`
- Modify: `internal/usertools/usertools.go`

- [ ] **Step 1: Update Validate's error message**

In `internal/usertools/usertools.go`, replace line 124:

```go
			return fmt.Errorf("secret %q: not set in .shell3/.env or environment", sec)
```

with:

```go
			return fmt.Errorf("secret %q: not set; run `shell3 secrets set --key %s --secret VALUE`", sec, sec)
```

Replace the `LoadAll` doc comment block (lines 49-52) — change:

```go
// LoadAll walks each dir in order and returns enabled, validated tools.
// Later dirs override earlier ones on name collision (project beats global).
// availableSecrets is the set of keys present in .env+OS env; tools that
// declare missing secrets are disabled with a warning.
```

to:

```go
// LoadAll walks each dir in order and returns enabled, validated tools.
// Later dirs override earlier ones on name collision (project beats global).
// availableSecrets is the set of keys present in the project secrets
// store; tools that declare missing secrets are disabled with a warning.
```

- [ ] **Step 2: Delete dotenv files**

```bash
rm internal/usertools/dotenv.go internal/usertools/dotenv_test.go
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/usertools/...`
Expected: PASS.

- [ ] **Step 4: Run full test suite**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add -A internal/usertools
git commit -m "refactor(usertools): drop .env loader, route secrets via secrets store"
```

---

### Task 10: README update

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Inspect current README**

Run: `grep -n "destroy\|.env" README.md` — note any line numbers referring to either.

- [ ] **Step 2: Add removal section + drop destroy/`.env` references**

Append (or place near install/init notes) the following section:

```markdown
### Removing a project's shell3 data

`rm -rf .shell3` from the project root. There is no `shell3 destroy` command.
```

If the README mentions `shell3 destroy` or `.shell3/.env` anywhere, replace those references:
- `shell3 destroy` → `rm -rf .shell3`
- `.shell3/.env` → "the project secrets store (`shell3 secrets set --key NAME --secret VALUE`)"

- [ ] **Step 3: Sanity build + grep**

Run:

```bash
grep -n "destroy\|\.env" README.md
```

Expected: no remaining references except the new "no `shell3 destroy` command" sentence.

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: project secrets + drop destroy notes"
```

---

### Task 11: End-to-end smoke

**Files:**
- (no source changes)

- [ ] **Step 1: Build clean binary**

```bash
go build -o /tmp/shell3 ./cmd/shell3
```

- [ ] **Step 2: Verify destroy is gone**

```bash
/tmp/shell3 --help | grep -c destroy
```
Expected: `0`.

- [ ] **Step 3: Verify secrets is registered**

```bash
/tmp/shell3 --help | grep -c secrets
```
Expected: `1` (line in Available Commands).

- [ ] **Step 4: Full lifecycle on a temp project**

```bash
TMPDIR=$(mktemp -d)
cd "$TMPDIR"
mkdir -p .shell3
/tmp/shell3 secrets set --key DEMO_TOKEN --secret supersecret123
/tmp/shell3 secrets list           # "DEMO_TOKEN  supersec***"
test -f .shell3/secrets.shell3
! grep -q supersecret123 .shell3/secrets.shell3   # plaintext must NOT appear
/tmp/shell3 secrets remove --key DEMO_TOKEN
/tmp/shell3 secrets list           # "No secrets configured."
cd - && rm -rf "$TMPDIR"
```

Expected: all assertions hold.

- [ ] **Step 5: Refuses outside inited project**

```bash
TMPDIR=$(mktemp -d)
cd "$TMPDIR"
/tmp/shell3 secrets list 2>&1 | grep -q "shell3 init"
echo $?
cd - && rm -rf "$TMPDIR"
```

Expected: prints `0` (error mentions `shell3 init`).

- [ ] **Step 6: Run full test suite one more time**

Run: `go test ./...`
Expected: PASS across all packages.

- [ ] **Step 7: No commit (verification only).**

---

## Self-Review

**Spec coverage check:**
- Storage path + wrap scheme — Task 1.
- `secrets.Load` requires inited project — Tasks 1, 4.
- CLI surface (`set --key --secret`, `list`, `remove --key`) — Task 5.
- Bare `shell3 secrets` prints help — Task 5 (cobra default behavior on a parent command with no `Run`/`RunE`).
- `list` masks last 3 chars — Task 5 (`maskSecret`).
- Drop `destroy` — Task 6.
- Wire secrets store into run, drop `os.Environ` overlay — Task 7.
- Drop `.env`/`.env.example`, gitignore `secrets.shell3` — Task 8.
- Delete `usertools/dotenv.go`, update Validate message — Task 9.
- README update — Task 10.
- End-to-end smoke — Task 11.

All spec sections covered.

**Placeholder scan:** No TBDs, no "implement later". Every code block is concrete. Test code is full, not abbreviated.

**Type consistency:** `secrets.Store` methods (`Load`, `Set`, `Get`, `Remove`, `List`, `All`) consistent across Tasks 1, 5, 7. `maskSecret` defined once in Task 5. `secretsMap` variable name used consistently in Task 7.

Plan is ready.
