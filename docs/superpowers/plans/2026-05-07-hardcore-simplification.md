# Shell3 Hardcore Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Delete the codex adapter, XOR obfuscation layer, and embedded model catalog; replace with two plain YAML files the user edits directly.

**Architecture:** `internal/config/authstore.go` replaces `credstore.go` — plain YAML read-only store. `internal/secrets/store.go` is rewritten with same interface over plain YAML. The `llm.Provider` interface drops `Auth()` and `Models()`; openai adapter moves to `internal/adapter/openai/`. All `models.ContextWindow()` calls replaced by lookups on `ModelChoice.ContextWindow`.

**Tech Stack:** Go stdlib `os`, `gopkg.in/yaml.v3` (already a dep), Cobra CLI, `$EDITOR` env var for auth UX.

**Spec:** `docs/superpowers/specs/2026-05-07-hardcore-simplification-design.md`

---

## File Map

### New files
| File | Purpose |
|------|---------|
| `internal/config/authstore.go` | AuthStore — plain YAML reader, types Instance/ModelDef/AuthFile |
| `internal/config/authstore_test.go` | Tests for AuthStore |
| `CLAUDE.md` | AI instructions (do not read ai-do-not-read.*) |

### Modified files
| File | Change |
|------|--------|
| `internal/config/config.go` | Update package doc |
| `internal/paths/paths.go` | Replace Credentials/Secrets paths with new YAML filenames |
| `internal/llm/provider.go` | Remove Auth/Models from Provider interface; CredStore→AuthStore |
| `internal/adapter/openai/register.go` | New path (was adapters/openai); use AuthStore |
| `internal/adapter/openai/client.go` | New path only (logic unchanged) |
| `internal/adapter/openai/internals_test.go` | New path only |
| `internal/secrets/store.go` | Full rewrite — plain YAML, same public interface |
| `internal/secrets/store_test.go` | New tests for plain-YAML store |
| `internal/chat/chat.go` | Add ContextWindow to ModelChoice; replace models.ContextWindow calls |
| `internal/chat/session.go` | Add contextWindowFor to reminderTracker |
| `internal/chat/session_test.go` | Supply contextWindowFor in tests |
| `cmd/shell3/auth.go` | Full rewrite — editor-based |
| `cmd/shell3/secrets.go` | Full rewrite — editor-based |
| `cmd/shell3/run.go` | Use AuthStore; remove Migrate; simplify resolveConnection |
| `cmd/shell3/main.go` | Remove codex blank import; update adapter import path |
| `Makefile` | Remove models-snapshot target and build dependency |
| `.gitignore` | Add `ai-do-not-read.*` |

### Deleted files/packages
| Path | |
|------|-|
| `internal/adapters/codex/` | Entire directory |
| `internal/adapters/openai/` | Replaced by internal/adapter/openai/ |
| `internal/obfile/` | Entire directory |
| `internal/obfuscate/` | Entire directory |
| `internal/models/` | Entire directory |
| `internal/config/credstore.go` | |
| `internal/config/credstore_test.go` | |
| `internal/config/migrate.go` | |
| `internal/config/migrate_test.go` | |

---

## Task 1: Create Feature Branch

**Files:** none

- [ ] **Step 1: Create and switch to feature branch**

```bash
git checkout -b simplify/auth-yaml
```

Expected: `Switched to a new branch 'simplify/auth-yaml'`

- [ ] **Step 2: Verify clean state**

```bash
git status
```

Expected: `nothing to commit, working tree clean`

---

## Task 2: Data Migration (Before Deleting Old Code)

Migrate existing credentials and secrets from obfuscated files to plain YAML. This runs while old packages still exist.

**Files:**
- Create: `cmd/migrate-once/main.go` (temp — deleted after use)
- Write: `~/.shell3/ai-do-not-read.auth.yaml`
- Write: `~/.shell3/ai-do-not-read.secrets.yaml`

- [ ] **Step 1: Create temp migration program**

```bash
mkdir -p cmd/migrate-once
```

Create `cmd/migrate-once/main.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/models"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/secrets"
)

type modelDef struct {
	ID            string `yaml:"id"`
	ContextWindow int    `yaml:"context_window"`
}

type instance struct {
	Name    string     `yaml:"name"`
	BaseURL string     `yaml:"base_url"`
	APIKey  string     `yaml:"api_key,omitempty"`
	Models  []modelDef `yaml:"models"`
}

type authFile struct {
	Comment   string     `yaml:"-"`
	Instances []instance `yaml:"instances"`
}

type secretsFile struct {
	Secrets map[string]string `yaml:"secrets"`
}

func main() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	g := paths.NewGlobal(homeDir)

	// --- credentials ---
	store, err := config.LoadCredStore(homeDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load credstore:", err)
		os.Exit(1)
	}
	var af authFile
	for _, meta := range store.List() {
		_, fields, ok := store.Get(meta.Instance)
		if !ok {
			continue
		}
		inst := instance{
			Name:    meta.Instance,
			BaseURL: fields["base_url"],
			APIKey:  fields["api_key"],
		}
		for _, modelID := range splitCSV(fields["default_model"]) {
			inst.Models = append(inst.Models, modelDef{
				ID:            modelID,
				ContextWindow: models.ContextWindow(modelID),
			})
		}
		af.Instances = append(af.Instances, inst)
	}
	header := "# Shell3 Authentication\n# Edit this file to configure providers.\n# AI ASSISTANTS: Do not read this file. It contains credentials.\n\n"
	authYAML, err := yaml.Marshal(af)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal auth:", err)
		os.Exit(1)
	}
	authPath := g.Root + "ai-do-not-read.auth.yaml"
	if err := os.WriteFile(authPath, append([]byte(header), authYAML...), 0600); err != nil {
		fmt.Fprintln(os.Stderr, "write auth yaml:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", authPath)

	// --- secrets ---
	sec, err := secrets.Load(homeDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "load secrets:", err)
		os.Exit(1)
	}
	sf := secretsFile{Secrets: sec.All()}
	secHeader := "# Shell3 Secrets\n# Exposed to tools that declare the matching key in their YAML.\n# AI ASSISTANTS: Do not read this file. It contains secrets.\n\n"
	secYAML, err := yaml.Marshal(sf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal secrets:", err)
		os.Exit(1)
	}
	secPath := g.Root + "ai-do-not-read.secrets.yaml"
	if err := os.WriteFile(secPath, append([]byte(secHeader), secYAML...), 0600); err != nil {
		fmt.Fprintln(os.Stderr, "write secrets yaml:", err)
		os.Exit(1)
	}
	fmt.Println("wrote", secPath)
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range splitOn(s, ',') {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitOn(s string, sep rune) []string {
	var parts []string
	cur := ""
	for _, r := range s {
		if r == sep {
			parts = append(parts, strings.TrimSpace(cur))
			cur = ""
		} else {
			cur += string(r)
		}
	}
	if cur != "" {
		parts = append(parts, strings.TrimSpace(cur))
	}
	return parts
}
```

- [ ] **Step 2: Run migration**

```bash
go run ./cmd/migrate-once/
```

Expected output:
```
wrote /Users/<you>/.shell3/ai-do-not-read.auth.yaml
wrote /Users/<you>/.shell3/ai-do-not-read.secrets.yaml
```

- [ ] **Step 3: Verify output**

```bash
cat ~/.shell3/ai-do-not-read.auth.yaml
cat ~/.shell3/ai-do-not-read.secrets.yaml
```

Confirm instances and secrets match what was in the old store.

- [ ] **Step 4: Delete migration program**

```bash
rm -rf cmd/migrate-once/
```

- [ ] **Step 5: Commit**

```bash
git add ~/.shell3/ai-do-not-read.auth.yaml ~/.shell3/ai-do-not-read.secrets.yaml 2>/dev/null || true
git commit -m "chore: data migration complete (no code changes yet)"
```

Note: the YAML files are in `~/.shell3/`, not in the repo — this commit may be empty. That's fine.

---

## Task 3: New AuthStore

**Files:**
- Create: `internal/config/authstore_test.go`
- Create: `internal/config/authstore.go`

- [ ] **Step 1: Write failing tests**

Create `internal/config/authstore_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func writeAuthYAML(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "ai-do-not-read.auth.yaml")
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

const testAuthYAML = `
instances:
  - name: myopenai
    base_url: https://api.openai.com/v1
    api_key: sk-test
    models:
      - id: gpt-4o
        context_window: 128000
      - id: o3
        context_window: 200000
  - name: ollama
    base_url: http://localhost:11434/v1
    models:
      - id: llama3.2
        context_window: 131072
`

func TestLoadAuthStore_MissingFile(t *testing.T) {
	dir := t.TempDir()
	store, err := config.LoadAuthStore(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(store.List()) != 0 {
		t.Errorf("expected empty store for missing file")
	}
}

func TestLoadAuthStore_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, err := config.LoadAuthStore(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	insts := store.List()
	if len(insts) != 2 {
		t.Fatalf("expected 2 instances, got %d", len(insts))
	}
}

func TestAuthStore_Get(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)

	inst, ok := store.Get("myopenai")
	if !ok {
		t.Fatal("expected to find myopenai")
	}
	if inst.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("wrong base_url: %q", inst.BaseURL)
	}
	if inst.APIKey != "sk-test" {
		t.Errorf("wrong api_key: %q", inst.APIKey)
	}
	if len(inst.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(inst.Models))
	}
	if inst.Models[0].ID != "gpt-4o" {
		t.Errorf("wrong first model: %q", inst.Models[0].ID)
	}
	if inst.Models[0].ContextWindow != 128000 {
		t.Errorf("wrong context_window: %d", inst.Models[0].ContextWindow)
	}
}

func TestAuthStore_Get_Missing(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent instance")
	}
}

func TestAuthStore_Get_NullAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)
	inst, ok := store.Get("ollama")
	if !ok {
		t.Fatal("expected to find ollama")
	}
	if inst.APIKey != "" {
		t.Errorf("expected empty api_key for ollama, got %q", inst.APIKey)
	}
}

func TestAuthStore_List_Order(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)
	insts := store.List()
	if insts[0].Name != "myopenai" {
		t.Errorf("expected first instance myopenai, got %q", insts[0].Name)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/config/... -run TestLoadAuthStore -v 2>&1 | head -20
```

Expected: compilation error (AuthStore not defined yet).

- [ ] **Step 3: Implement AuthStore**

Create `internal/config/authstore.go`:

```go
package config

import (
	"errors"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ModelDef is one model entry in the auth YAML.
type ModelDef struct {
	ID            string `yaml:"id"`
	ContextWindow int    `yaml:"context_window"`
}

// Instance is one configured provider in the auth YAML.
type Instance struct {
	Name    string     `yaml:"name"`
	BaseURL string     `yaml:"base_url"`
	APIKey  string     `yaml:"api_key,omitempty"`
	Models  []ModelDef `yaml:"models"`
}

type authFile struct {
	Instances []Instance `yaml:"instances"`
}

// AuthStore reads ~/.shell3/ai-do-not-read.auth.yaml.
// It is read-only; the user edits the file directly.
type AuthStore struct {
	data authFile
}

// LoadAuthStore reads the auth YAML from homeDir. Returns an empty store if
// the file does not exist.
func LoadAuthStore(homeDir string) (*AuthStore, error) {
	path := filepath.Join(homeDir, ".shell3", "ai-do-not-read.auth.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &AuthStore{}, nil
		}
		return nil, err
	}
	var af authFile
	if err := yaml.Unmarshal(data, &af); err != nil {
		return nil, err
	}
	return &AuthStore{data: af}, nil
}

// Get returns the Instance with the given name.
func (s *AuthStore) Get(name string) (Instance, bool) {
	for _, inst := range s.data.Instances {
		if inst.Name == name {
			return inst, true
		}
	}
	return Instance{}, false
}

// List returns all configured instances in file order.
func (s *AuthStore) List() []Instance {
	out := make([]Instance, len(s.data.Instances))
	copy(out, s.data.Instances)
	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/... -run TestLoadAuthStore -run TestAuthStore -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/authstore.go internal/config/authstore_test.go
git commit -m "feat: add AuthStore — plain YAML credential reader"
```

---

## Task 4: New Secrets Store

**Files:**
- Create: `internal/secrets/store_test.go`
- Modify: `internal/secrets/store.go` (full rewrite)

- [ ] **Step 1: Write failing tests**

Create `internal/secrets/store_test.go`:

```go
package secrets_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/secrets"
)

func setupSecrets(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if content != "" {
		p := filepath.Join(dir, "ai-do-not-read.secrets.yaml")
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

const testSecretsYAML = `
secrets:
  GITHUB_TOKEN: ghp_abc123
  BRAVE_API: brave_xyz789
`

func TestSecretsLoad_Missing(t *testing.T) {
	home := setupSecrets(t, "")
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(s.List()) != 0 {
		t.Error("expected empty store")
	}
}

func TestSecretsLoad_Valid(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := s.List()
	if len(names) != 2 {
		t.Fatalf("expected 2 secrets, got %d", len(names))
	}
}

func TestSecretsGet(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	v, ok := s.Get("GITHUB_TOKEN")
	if !ok {
		t.Fatal("expected GITHUB_TOKEN to exist")
	}
	if v != "ghp_abc123" {
		t.Errorf("wrong value: %q", v)
	}
}

func TestSecretsSet(t *testing.T) {
	home := setupSecrets(t, "")
	s, _ := secrets.Load(home)
	if err := s.Set("MY_KEY", "my_val"); err != nil {
		t.Fatal(err)
	}
	v, ok := s.Get("MY_KEY")
	if !ok || v != "my_val" {
		t.Errorf("expected MY_KEY=my_val after Set, got ok=%v v=%q", ok, v)
	}
	// reload from disk
	s2, _ := secrets.Load(home)
	v2, ok2 := s2.Get("MY_KEY")
	if !ok2 || v2 != "my_val" {
		t.Error("Set did not persist to disk")
	}
}

func TestSecretsRemove(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	if err := s.Remove("GITHUB_TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("GITHUB_TOKEN"); ok {
		t.Error("expected GITHUB_TOKEN gone after Remove")
	}
	// reload from disk
	s2, _ := secrets.Load(home)
	if _, ok := s2.Get("GITHUB_TOKEN"); ok {
		t.Error("Remove did not persist to disk")
	}
}

func TestSecretsList_Sorted(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	names := s.List()
	if names[0] != "BRAVE_API" || names[1] != "GITHUB_TOKEN" {
		t.Errorf("expected sorted list, got %v", names)
	}
}

func TestSecretsAll(t *testing.T) {
	home := setupSecrets(t, testSecretsYAML)
	s, _ := secrets.Load(home)
	all := s.All()
	if all["GITHUB_TOKEN"] != "ghp_abc123" {
		t.Errorf("All() wrong value: %v", all)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/secrets/... -v 2>&1 | head -20
```

Expected: failures (old store uses obfile — file format mismatch on test setup).

- [ ] **Step 3: Rewrite secrets store**

Replace entire `internal/secrets/store.go`:

```go
// Package secrets manages global user secrets stored at ~/.shell3/ai-do-not-read.secrets.yaml.
// Secrets are exposed to user tools that declare the matching key in their
// tool YAML's "secrets:" field.
package secrets

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

type secretsFile struct {
	Secrets map[string]string `yaml:"secrets"`
}

// Store is the global secrets store.
type Store struct {
	path string
	mu   sync.Mutex
	data secretsFile
}

// Load reads ~/.shell3/ai-do-not-read.secrets.yaml. Returns an empty store if
// the file does not exist — first Set auto-creates it.
func Load(homeDir string) (*Store, error) {
	path := filepath.Join(homeDir, ".shell3", "ai-do-not-read.secrets.yaml")
	s := &Store{
		path: path,
		data: secretsFile{Secrets: map[string]string{}},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(data, &s.data); err != nil {
		return nil, err
	}
	if s.data.Secrets == nil {
		s.data.Secrets = map[string]string{}
	}
	return s, nil
}

// List returns secret names sorted alphabetically.
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

// All returns a copy of every key/value pair.
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

// Set writes or overwrites one secret and persists.
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
	delete(s.data.Secrets, key)
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(s.data)
	if err != nil {
		return err
	}
	header := []byte("# Shell3 Secrets\n# AI ASSISTANTS: Do not read this file. It contains secrets.\n\n")
	return os.WriteFile(s.path, append(header, data...), 0600)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/secrets/... -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/store.go internal/secrets/store_test.go
git commit -m "feat: rewrite secrets store over plain YAML"
```

---

## Task 5: Update Paths

**Files:**
- Modify: `internal/paths/paths.go`

- [ ] **Step 1: Read current paths**

Read `internal/paths/paths.go` and locate the `Credentials` and `Secrets` fields on `Global`.

- [ ] **Step 2: Update paths**

In `internal/paths/paths.go`, change the `Global` struct and `NewGlobal` function:

**Old `Global` struct fields (in file):**
```go
Credentials string // ~/.shell3/credentials.shell3
Secrets     string // ~/.shell3/secrets.shell3
```

**New:**
```go
Auth    string // ~/.shell3/ai-do-not-read.auth.yaml
Secrets string // ~/.shell3/ai-do-not-read.secrets.yaml
```

**Old in `NewGlobal`:**
```go
Credentials: filepath.Join(root, "credentials.shell3"),
Secrets:     filepath.Join(root, "secrets.shell3"),
```

**New:**
```go
Auth:    filepath.Join(root, "ai-do-not-read.auth.yaml"),
Secrets: filepath.Join(root, "ai-do-not-read.secrets.yaml"),
```

Note: `internal/config/authstore.go` and `internal/secrets/store.go` currently build the path inline with `filepath.Join(homeDir, ".shell3", "ai-do-not-read.*.yaml")` — update them to use `paths.NewGlobal(homeDir).Auth` and `paths.NewGlobal(homeDir).Secrets` respectively after this step.

- [ ] **Step 3: Update authstore.go to use paths**

In `internal/config/authstore.go`, replace:
```go
path := filepath.Join(homeDir, ".shell3", "ai-do-not-read.auth.yaml")
```
with:
```go
path := paths.NewGlobal(homeDir).Auth
```
Add import `"github.com/weatherjean/shell3/internal/paths"`.

- [ ] **Step 4: Update secrets/store.go to use paths**

In `internal/secrets/store.go`, replace:
```go
path := filepath.Join(homeDir, ".shell3", "ai-do-not-read.secrets.yaml")
```
with:
```go
path := paths.NewGlobal(homeDir).Secrets
```
Add import `"github.com/weatherjean/shell3/internal/paths"`. Remove `"path/filepath"` if no longer used.

- [ ] **Step 5: Build check**

```bash
go build ./internal/paths/... ./internal/config/... ./internal/secrets/...
```

Expected: no errors. (Will error elsewhere if other packages reference old `g.Credentials` — fix those in later tasks.)

- [ ] **Step 6: Run tests**

```bash
go test ./internal/config/... ./internal/secrets/...
```

Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/paths/paths.go internal/config/authstore.go internal/secrets/store.go
git commit -m "feat: update paths for plain YAML auth and secrets files"
```

---

## Task 6: Update llm.Provider Interface

**Files:**
- Modify: `internal/llm/provider.go`

The interface currently takes `*config.CredStore` and includes `Auth()` and `Models()`. After this task it takes `*config.AuthStore` and only has `Name()`, `SingleInstance()`, `NewClient()`.

- [ ] **Step 1: Read current provider.go**

Read `internal/llm/provider.go`.

- [ ] **Step 2: Update the interface**

Replace the `Provider` interface definition:

**Old:**
```go
type Provider interface {
    Name() string
    SingleInstance() bool
    Auth(ctx context.Context, w io.Writer, store *config.CredStore, instance string) error
    NewClient(ctx context.Context, store *config.CredStore, instance, model string) (Streamer, error)
    Models(store *config.CredStore, instance string) []string
}
```

**New:**
```go
type Provider interface {
    Name() string
    SingleInstance() bool
    NewClient(ctx context.Context, store *config.AuthStore, instance, model string) (Streamer, error)
}
```

Update the import: `"github.com/weatherjean/shell3/internal/config"` stays. Remove unused imports (`io` if only used by `Auth`).

- [ ] **Step 3: Build check (expect failures — fix in next tasks)**

```bash
go build ./internal/llm/... 2>&1
```

Expected: PASS for llm package itself.

```bash
go build ./... 2>&1 | head -30
```

Expected: failures in adapters and cmd — these are fixed in later tasks.

- [ ] **Step 4: Commit**

```bash
git add internal/llm/provider.go
git commit -m "refactor: simplify Provider interface — drop Auth/Models, use AuthStore"
```

---

## Task 7: Rename and Update OpenAI Adapter

Move `internal/adapters/openai/` → `internal/adapter/openai/` and update to use `AuthStore`.

**Files:**
- Create: `internal/adapter/openai/register.go`
- Create: `internal/adapter/openai/client.go` (copy + no logic changes)
- Create: `internal/adapter/openai/internals_test.go` (copy + path update)
- Delete: `internal/adapters/openai/` (after copy)

- [ ] **Step 1: Create new directory**

```bash
mkdir -p internal/adapter/openai
```

- [ ] **Step 2: Copy client.go and test to new location**

```bash
cp internal/adapters/openai/client.go internal/adapter/openai/client.go
cp internal/adapters/openai/internals_test.go internal/adapter/openai/internals_test.go
```

In both files, update the package declaration if needed (should already be `package openai`). Update any import paths from `internal/adapters/openai` to `internal/adapter/openai` if they appear. (They likely don't self-reference, but verify.)

- [ ] **Step 3: Write new register.go**

Create `internal/adapter/openai/register.go`:

```go
package openai

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

type provider struct{}

func init() { llm.Register("openai", &provider{}) }

func (*provider) Name() string         { return "openai" }
func (*provider) SingleInstance() bool { return false }

// NewClient reads the instance from store and builds a Client.
func (*provider) NewClient(_ context.Context, store *config.AuthStore, instance, model string) (llm.Streamer, error) {
	inst, ok := store.Get(instance)
	if !ok {
		return nil, fmt.Errorf("openai: no instance %q — edit ~/.shell3/ai-do-not-read.auth.yaml", instance)
	}
	if model == "" && len(inst.Models) > 0 {
		model = inst.Models[0].ID
	}
	return NewClient(inst.BaseURL, inst.APIKey, model), nil
}

// modelIDs returns the model ID list for an instance.
func modelIDs(store *config.AuthStore, instance string) []string {
	inst, ok := store.Get(instance)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(inst.Models))
	for _, m := range inst.Models {
		out = append(out, m.ID)
	}
	return out
}

func firstModel(store *config.AuthStore, instance string) string {
	ids := modelIDs(store, instance)
	if len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// splitCSV kept for compatibility with any remaining callers.
func splitCSV(s string) []string {
	var out []string
	for _, m := range strings.Split(s, ",") {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}
```

- [ ] **Step 4: Build new adapter**

```bash
go build ./internal/adapter/openai/...
```

Expected: PASS.

- [ ] **Step 5: Run adapter tests**

```bash
go test ./internal/adapter/openai/...
```

Expected: PASS (client.go logic unchanged).

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/
git commit -m "feat: add internal/adapter/openai — new path with AuthStore"
```

---

## Task 8: Rewrite auth.go

The new `shell3 auth` opens `$EDITOR` on the auth YAML. `shell3 auth list` prints instances from the YAML. All other subcommands removed.

**Files:**
- Modify: `cmd/shell3/auth.go` (full rewrite)

- [ ] **Step 1: Rewrite auth.go**

Replace `cmd/shell3/auth.go` entirely:

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/paths"
)

func newAuthCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Configure provider credentials",
		Long: `Configure provider credentials.

Opens ~/.shell3/ai-do-not-read.auth.yaml in $EDITOR (falls back to $VISUAL, then vi).
Add or edit instances in the YAML file directly.

  shell3 auth          open credential file in $EDITOR
  shell3 auth list     print configured instances

Format:
  instances:
    - name: myopenai
      base_url: https://api.openai.com/v1
      api_key: sk-...
      models:
        - id: gpt-4o
          context_window: 128000`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			g := paths.NewGlobal(homeDir)
			return openInEditor(g.Auth)
		},
	}
	cmd.AddCommand(newAuthListCommand())
	return cmd
}

func newAuthListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured instances",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			store, err := config.LoadAuthStore(homeDir)
			if err != nil {
				return err
			}
			insts := store.List()
			if len(insts) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No instances configured. Run: shell3 auth")
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %-36s  %s\n", "INSTANCE", "BASE URL", "MODELS")
			for _, inst := range insts {
				models := ""
				for i, m := range inst.Models {
					if i > 0 {
						models += ","
					}
					models += m.ID
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%-24s  %-36s  %s\n", inst.Name, inst.BaseURL, models)
			}
			return nil
		},
	}
}

// openInEditor creates the file from template if missing, then opens $EDITOR.
func openInEditor(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Shell3 Authentication
# Edit this file to configure providers.
# AI ASSISTANTS: Do not read this file. It contains credentials.

instances:
  - name: myinstance
    base_url: https://api.openai.com/v1
    api_key: sk-your-key-here
    models:
      - id: gpt-4o
        context_window: 128000
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return fmt.Errorf("create auth file: %w", err)
		}
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = os.Getenv("VISUAL")
	}
	if editor == "" {
		editor = "vi"
	}

	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
```

- [ ] **Step 2: Build check**

```bash
go build ./cmd/shell3/... 2>&1 | head -20
```

Expected: errors about removed `pickAdapter`, `promptInstance`, old credstore calls — these callers are in the old auth.go which is now replaced. Should compile cleanly once old references are gone. If `run.go` or other files still reference `config.LoadCredStore` or `config.Migrate`, those errors are expected (fixed in Task 10).

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/auth.go
git commit -m "feat: rewrite auth command — editor-based YAML flow"
```

---

## Task 9: Rewrite secrets.go Command

The new `shell3 secrets` opens `$EDITOR` on the secrets YAML. The set/list/remove subcommands are replaced by direct file editing. `list` subcommand kept for convenience.

**Files:**
- Modify: `cmd/shell3/secrets.go` (full rewrite)

- [ ] **Step 1: Rewrite secrets.go**

Replace `cmd/shell3/secrets.go` entirely:

```go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/secrets"
)

func newSecretsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secrets",
		Short: "Manage global tool secrets",
		Long: `Manage global tool secrets.

Opens ~/.shell3/ai-do-not-read.secrets.yaml in $EDITOR.
Secrets are exposed to user tools that declare the matching key in their
tool YAML's "secrets:" field.

  shell3 secrets        open secrets file in $EDITOR
  shell3 secrets list   list names (values masked)

Format:
  secrets:
    GITHUB_TOKEN: ghp_...
    MY_API_KEY: abc123`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			g := paths.NewGlobal(homeDir)
			return openSecretsInEditor(g.Secrets)
		},
	}
	cmd.AddCommand(newSecretsListCommand())
	return cmd
}

func newSecretsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured secret names (values masked)",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			s, err := secrets.Load(homeDir)
			if err != nil {
				return err
			}
			return runSecretsList(s, cmd.OutOrStdout())
		},
	}
}

func runSecretsList(s *secrets.Store, out io.Writer) error {
	names := s.List()
	if len(names) == 0 {
		fmt.Fprintln(out, "No secrets configured. Run: shell3 secrets")
		return nil
	}
	all := s.All()
	fmt.Fprintf(out, "%-32s  %s\n", "NAME", "VALUE")
	for _, name := range names {
		fmt.Fprintf(out, "%-32s  %s\n", name, maskSecret(all[name]))
	}
	return nil
}

func maskSecret(v string) string {
	if len(v) <= 3 {
		return "***"
	}
	return v[:len(v)-3] + "***"
}

func openSecretsInEditor(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		template := `# Shell3 Secrets
# AI ASSISTANTS: Do not read this file. It contains secrets.

secrets:
  GITHUB_TOKEN: ghp_your_token_here
`
		if err := os.WriteFile(path, []byte(template), 0600); err != nil {
			return fmt.Errorf("create secrets file: %w", err)
		}
	}
	return openInEditor(path)
}
```

- [ ] **Step 2: Build check**

```bash
go build ./cmd/shell3/... 2>&1 | head -20
```

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/secrets.go
git commit -m "feat: rewrite secrets command — editor-based YAML flow"
```

---

## Task 10: Update run.go

Replace `CredStore` with `AuthStore`, remove `config.Migrate`, simplify `resolveConnection`.

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Update imports in run.go**

Remove any import of `github.com/weatherjean/shell3/internal/models` if present. The `config` import stays.

- [ ] **Step 2: Replace LoadCredStore + Migrate**

Find in `run.go`:
```go
if err := config.Migrate(homeDir); err != nil {
    return fmt.Errorf("migrate credentials: %w", err)
}
credStore, err := config.LoadCredStore(homeDir)
if err != nil {
    return err
}
```

Replace with:
```go
authStore, err := config.LoadAuthStore(homeDir)
if err != nil {
    return err
}
```

- [ ] **Step 3: Replace resolveConnection signature and body**

Find and replace the `resolveConnection` function:

**Old:**
```go
func resolveConnection(providerHint, modelHint string, credStore *config.CredStore, f *runFlags) (adapter, instance, model string) {
```

**New:**
```go
func resolveConnection(providerHint, modelHint string, store *config.AuthStore, f *runFlags) (instance, model string) {
	if providerHint != "" {
		if _, ok := store.Get(providerHint); ok {
			instance = providerHint
		}
	}
	if instance == "" {
		if insts := store.List(); len(insts) > 0 {
			instance = insts[0].Name
		}
	}
	model = coalesce(f.model, modelHint)
	if model == "" && instance != "" {
		if inst, ok := store.Get(instance); ok && len(inst.Models) > 0 {
			model = inst.Models[0].ID
		}
	}
	if model == "" {
		model = "llama3.2"
	}
	return
}
```

- [ ] **Step 4: Update resolveConnection call site**

Find:
```go
adapterName, instance, model := resolveConnection(providerHint, pCfg.Model, credStore, f)
if adapterName == "" {
    return fmt.Errorf("no adapter configured — run: shell3 auth")
}
prov, ok := llm.Get(adapterName)
if !ok {
    return fmt.Errorf("unknown adapter %q (registered: %v)", adapterName, llm.Registered())
}
```

Replace with:
```go
instance, model := resolveConnection(providerHint, pCfg.Model, authStore, f)
if instance == "" {
    return fmt.Errorf("no provider configured — run: shell3 auth")
}
prov, ok := llm.Get("openai")
if !ok {
    return fmt.Errorf("openai adapter not registered")
}
```

- [ ] **Step 5: Update models list builder**

Find the block that builds `var models []chat.ModelChoice` using `credStore.List()` and `p.Models()`. Replace with:

```go
var models []chat.ModelChoice
for _, inst := range authStore.List() {
    for _, m := range inst.Models {
        models = append(models, chat.ModelChoice{
            Provider:      inst.Name,
            Model:         m.ID,
            ContextWindow: m.ContextWindow,
        })
    }
}
if len(models) == 0 {
    models = []chat.ModelChoice{{Provider: instance, Model: model}}
}
```

Note: `chat.ModelChoice` gains `ContextWindow` in Task 11 — this will compile after that task.

- [ ] **Step 6: Update buildClient closure**

Find the `buildClient` closure that calls `credStore.Get()`. Replace with:

```go
buildClient := func(inst, m string) (chat.LLMClient, error) {
    return prov.NewClient(ctx, authStore, inst, m)
}
```

- [ ] **Step 7: Update streamer construction**

Find:
```go
streamer, err := prov.NewClient(ctx, credStore, instance, model)
```

Replace with:
```go
streamer, err := prov.NewClient(ctx, authStore, instance, model)
```

- [ ] **Step 8: Build check**

```bash
go build ./cmd/shell3/... 2>&1 | head -30
```

Expected: errors about `chat.ModelChoice` missing `ContextWindow` field (fixed in Task 11) and possibly remaining `credStore` references. Fix any remaining `credStore` references found.

- [ ] **Step 9: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "refactor: run.go — use AuthStore, remove Migrate, simplify provider resolution"
```

---

## Task 11: Update Chat Package

Add `ContextWindow` to `ModelChoice`, replace `models.ContextWindow` calls with per-instance lookup.

**Files:**
- Modify: `internal/chat/chat.go`
- Modify: `internal/chat/session.go`
- Modify: `internal/chat/session_test.go`

- [ ] **Step 1: Add ContextWindow to ModelChoice**

In `internal/chat/chat.go`, find:
```go
type ModelChoice struct {
	Provider string
	Model    string
}
```

Replace with:
```go
type ModelChoice struct {
	Provider      string
	Model         string
	ContextWindow int
}
```

- [ ] **Step 2: Add contextWindowFor helper in chat.go**

Add this unexported function anywhere in `internal/chat/chat.go`:

```go
func contextWindowFor(models []ModelChoice, id string) int {
	for _, m := range models {
		if m.Model == id {
			return m.ContextWindow
		}
	}
	return 0
}
```

- [ ] **Step 3: Replace models.ContextWindow calls in chat.go**

Find:
```go
if _, initModel := splitStatus(cfg.StatusLine); initModel != "" {
    app.SetContextWindow(models.ContextWindow(initModel))
}
```

Replace with:
```go
if _, initModel := splitStatus(cfg.StatusLine); initModel != "" {
    app.SetContextWindow(contextWindowFor(cfg.Models, initModel))
}
```

Find (model switch handler):
```go
app.SetContextWindow(models.ContextWindow(choice.Model))
```

Replace with:
```go
app.SetContextWindow(choice.ContextWindow)
```

Remove `"github.com/weatherjean/shell3/internal/models"` from chat.go imports.

- [ ] **Step 4: Update reminderTracker in session.go**

In `internal/chat/session.go`, find:
```go
type reminderTracker struct {
	lastContextPct int
	lastModel      string
	lastTokens     int
}
```

Replace with:
```go
type reminderTracker struct {
	lastContextPct   int
	lastModel        string
	lastTokens       int
	contextWindowFor func(string) int
}
```

Find in `reminderTracker.check`:
```go
contextWindow := models.ContextWindow(model)
```

Replace with:
```go
contextWindow := 0
if r.contextWindowFor != nil {
    contextWindow = r.contextWindowFor(model)
}
```

Remove `"github.com/weatherjean/shell3/internal/models"` from session.go imports.

- [ ] **Step 5: Wire contextWindowFor in chat.go**

In `RunInteractive`, immediately after creating `sess := &session{}`, add:

```go
sess.reminders.contextWindowFor = func(id string) int {
    return contextWindowFor(cfg.Models, id)
}
```

- [ ] **Step 6: Update session_test.go**

In `internal/chat/session_test.go`, add a package-level helper:

```go
var testContextWindowFor = func(m string) int {
    return map[string]int{
        "claude-sonnet-4-6": 1_000_000,
        "claude-opus-4-7":   1_000_000,
        "gpt-4o":            128_000,
    }[m]
}
```

In each test that exercises context-window behavior (`TestReminderTracker_ContextBucket`, `TestReminderTracker_NoRepeatSameBucket`, `TestReminderTracker_30kDeltaThreshold`), add before the first `r.check` call:

```go
r.contextWindowFor = testContextWindowFor
```

For `TestReminderTracker_30kDeltaThreshold`, the `r2` struct literal becomes:
```go
r2 := reminderTracker{
    lastModel:        "claude-sonnet-4-6",
    lastContextPct:   10,
    lastTokens:       100_000,
    contextWindowFor: testContextWindowFor,
}
```

Remove `"github.com/weatherjean/shell3/internal/models"` from session_test.go imports if present.

- [ ] **Step 7: Build and test**

```bash
go build ./internal/chat/... && go test ./internal/chat/... -v
```

Expected: all PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/chat/chat.go internal/chat/session.go internal/chat/session_test.go
git commit -m "refactor: replace models.ContextWindow with per-ModelChoice lookup"
```

---

## Task 12: Update main.go

**Files:**
- Modify: `cmd/shell3/main.go`

- [ ] **Step 1: Update blank imports**

In `cmd/shell3/main.go`, find:
```go
_ "github.com/weatherjean/shell3/internal/adapters/codex"
_ "github.com/weatherjean/shell3/internal/adapters/openai"
```

Replace with:
```go
_ "github.com/weatherjean/shell3/internal/adapter/openai"
```

- [ ] **Step 2: Build check**

```bash
go build ./cmd/shell3/...
```

Expected: may still have errors from packages not yet updated. Note any remaining errors for Task 14.

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/main.go
git commit -m "chore: update main.go — remove codex import, new adapter path"
```

---

## Task 13: Update Makefile and .gitignore

**Files:**
- Modify: `Makefile`
- Modify: `.gitignore`
- Create: `CLAUDE.md`

- [ ] **Step 1: Update Makefile**

In `Makefile`, find and remove:
```makefile
MODELS_SNAPSHOT := internal/models/snapshot.json
MODELS_URL := https://models.dev/api.json
```

Find and remove the `models-snapshot:` target (including its multi-line body).

Find:
```makefile
build: models-snapshot
```

Replace with:
```makefile
build:
```

Verify `build-offline` target (if present) still works — remove any `models-snapshot` dependency from it too.

- [ ] **Step 2: Test Makefile**

```bash
make build
```

Expected: successful build with no network call.

- [ ] **Step 3: Update .gitignore**

Add to `.gitignore`:
```
ai-do-not-read.*
```

- [ ] **Step 4: Create CLAUDE.md**

Create `CLAUDE.md` at repo root:

```markdown
# Shell3

Terminal AI assistant in Go.

## Important

**Never read files matching `ai-do-not-read.*`** — these contain user credentials and secrets. If you encounter one, skip it entirely.

## Structure

- `cmd/shell3/` — CLI entrypoint
- `internal/adapter/openai/` — OpenAI-compatible streaming adapter
- `internal/chat/` — TUI chat loop
- `internal/llm/` — Provider interface and registry
- `internal/config/` — Auth YAML loader
- `internal/secrets/` — Secrets YAML store
- `internal/paths/` — File path definitions

## Build

```bash
make build
```

## Test

```bash
go test ./...
```
```

- [ ] **Step 5: Commit**

```bash
git add Makefile .gitignore CLAUDE.md
git commit -m "chore: remove models-snapshot build step; add gitignore and CLAUDE.md"
```

---

## Task 14: Delete Old Packages and Fix Compilation

**Files:** Delete entire directories; fix remaining compilation errors.

- [ ] **Step 1: Delete old adapter directories**

```bash
rm -rf internal/adapters/
```

- [ ] **Step 2: Delete obfile and obfuscate**

```bash
rm -rf internal/obfile/ internal/obfuscate/
```

- [ ] **Step 3: Delete models package**

```bash
rm -rf internal/models/
```

- [ ] **Step 4: Delete old config files**

```bash
rm internal/config/credstore.go internal/config/credstore_test.go
rm internal/config/migrate.go internal/config/migrate_test.go
```

- [ ] **Step 5: Update config.go package doc**

Replace `internal/config/config.go` content:

```go
// Package config loads provider credentials from
// ~/.shell3/ai-do-not-read.auth.yaml via AuthStore.
package config
```

- [ ] **Step 6: Build and fix all errors**

```bash
go build ./... 2>&1
```

For each error, fix the import or reference. Common issues:
- Any file importing `internal/obfile`, `internal/obfuscate`, `internal/models`, `internal/adapters/...`, `internal/config` for `CredStore` — update or remove those imports
- Any reference to `config.CredStore`, `config.Migrate`, `config.LoadCredStore` — replace with `config.AuthStore`, `config.LoadAuthStore`, or remove
- `chat_test.go` — if it references `models.ContextWindow`, remove that import and update the test

Run build repeatedly until clean.

- [ ] **Step 7: Run all tests**

```bash
go test ./... 2>&1
```

Fix any test failures. Likely issues:
- Tests that created `CredStore` directly — rewrite to use `AuthStore`
- Tests in `internal/chat/chat_test.go` that reference `models` package

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "feat: delete codex adapter, obfile, obfuscate, models, old credstore"
```

---

## Task 15: Full Test Pass and Trace Audit

- [ ] **Step 1: Run all tests**

```bash
go test ./... -v 2>&1 | tail -30
```

Expected: all PASS, zero FAIL.

- [ ] **Step 2: Trace audit — grep for deleted symbols**

```bash
grep -r "credstore\|CredStore\|LoadCredStore\|obfile\|obfuscate\|snapshot\|models\.Context\|adapters/codex\|adapters/openai\|config\.Migrate\|Migrate(" . --include="*.go" --include="*.md" --include="Makefile" -l
```

Expected: no output. If any files appear, open them and remove all traces.

```bash
grep -r "credentials\.shell3\|secrets\.shell3\|codex_tokens\|obfuscated" . --include="*.go" --include="*.md" --include="*.yaml" -l
```

Expected: no output.

- [ ] **Step 3: Verify deleted directories are gone**

```bash
ls internal/adapters/ 2>&1
ls internal/obfile/ 2>&1
ls internal/obfuscate/ 2>&1
ls internal/models/ 2>&1
```

Expected: `No such file or directory` for all four.

- [ ] **Step 4: Verify build is clean**

```bash
go build ./...
```

Expected: no output (success).

- [ ] **Step 5: Manual smoke test**

```bash
go run ./cmd/shell3/ auth list
go run ./cmd/shell3/ secrets list
go run ./cmd/shell3/ doctor 2>/dev/null || true
```

Verify `auth list` shows configured instances from the new YAML. Verify `secrets list` shows secrets.

- [ ] **Step 6: Commit trace audit fixes (if any)**

```bash
git add -A
git commit -m "chore: trace audit — remove all references to deleted packages"
```

---

## Task 16: Merge Prep

- [ ] **Step 1: Final test run**

```bash
go test ./...
```

Expected: all PASS.

- [ ] **Step 2: Final build**

```bash
make build
```

Expected: clean build, no network calls.

- [ ] **Step 3: Verify branch policy checklist**

Confirm all of the following before merging to main:
- [ ] No dead imports (`go build ./...` clean)
- [ ] All tests pass (`go test ./...` all PASS)
- [ ] Manual smoke: `shell3 auth list`, `shell3 secrets list`, one chat turn
- [ ] No references to `credstore`, `obfile`, `obfuscate`, `internal/models`, `adapters/codex` (trace audit passed)
- [ ] `~/.shell3/ai-do-not-read.auth.yaml` and `ai-do-not-read.secrets.yaml` contain migrated data
- [ ] `ai-do-not-read.*` in `.gitignore`
- [ ] `CLAUDE.md` created

- [ ] **Step 4: Merge to main**

```bash
git checkout main
git merge simplify/auth-yaml
```
