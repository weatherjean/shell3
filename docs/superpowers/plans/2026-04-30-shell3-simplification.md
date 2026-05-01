# Shell3 Simplification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify shell3's config model (global home dir + project UUID ref), unify duplicate storage code, clean up dead code, and add `doctor` + auto-bootstrap — resulting in open-source-quality, well-tested code with no `init` command.

**Architecture:** A new `Paths` struct centralizes all path construction. An `obfile` package extracts duplicated obfuscated-file I/O shared by CredStore and secrets. A UUID-based `.ref` file in `.shell3/` bridges each project to its personal state in `~/.shell3/projects/<uuid>/` (DB, secrets). The `run` command auto-bootstraps on first use; `init` is deleted.

**Tech Stack:** Go 1.25, cobra, gopkg.in/yaml.v3, modernc.org/sqlite, github.com/google/uuid (promote to direct dep)

---

## File Map

### New Files
| File | Responsibility |
|------|---------------|
| `internal/paths/paths.go` | `Global`, `Project`, `Local` structs; all path construction |
| `internal/paths/paths_test.go` | Path construction tests |
| `internal/obfile/obfile.go` | `Read`/`Write` for XOR-obfuscated YAML files (atomic) |
| `internal/obfile/obfile_test.go` | Round-trip and error case tests |
| `internal/ref/ref.go` | `.ref` UUID file + `meta.json` creation/loading |
| `internal/ref/ref_test.go` | Init, load, FindByCWD tests |
| `cmd/shell3/doctor.go` | `shell3 doctor` command |

### Modified Files
| File | Change |
|------|--------|
| `internal/config/credstore.go` | Use `obfile.Read`/`Write`; drop `Version` field |
| `internal/config/credstore_test.go` | Verify still passes |
| `internal/secrets/store.go` | Use `obfile`; load from global (`~/.shell3/`), not cwd |
| `internal/secrets/store_test.go` | Update to global path |
| `internal/hooks/hooks.go` | Consolidate 4 `callHook*` fns → 1 with `dispatchMode` |
| `internal/hooks/hooks_test.go` | Verify behavior preserved |
| `internal/persona/persona.go` | `ParseConfig` returns body bytes; `Load` accepts them |
| `internal/usertools/usertools.go` | Drop `Before`/`After`; fix `reservedNames` from persona |
| `internal/scaffold/scaffold.go` | Rename `base.md`→`code.md`; drop plans/; simplify gitignore |
| `cmd/shell3/run.go` | Use `Paths`; auto-bootstrap; fix persona default `"code"` |
| `cmd/shell3/main.go` | Remove `init`; add `doctor` |
| `cmd/shell3/secrets.go` | Load from global (homeDir) not cwd |

### Deleted Files
| File | Reason |
|------|--------|
| `cmd/shell3/init.go` | Replaced by auto-bootstrap |

---

## Task 1: `internal/paths` — centralize path construction

**Files:**
- Create: `internal/paths/paths.go`
- Create: `internal/paths/paths_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/paths/paths_test.go
package paths_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

func TestGlobal(t *testing.T) {
	g := paths.NewGlobal("/home/user")
	if g.Root != "/home/user/.shell3" {
		t.Fatalf("Root: got %q", g.Root)
	}
	if g.Credentials != "/home/user/.shell3/credentials.shell3" {
		t.Fatalf("Credentials: got %q", g.Credentials)
	}
	if g.Secrets != "/home/user/.shell3/secrets.shell3" {
		t.Fatalf("Secrets: got %q", g.Secrets)
	}
	if g.Projects != "/home/user/.shell3/projects" {
		t.Fatalf("Projects: got %q", g.Projects)
	}
}

func TestProject(t *testing.T) {
	g := paths.NewGlobal("/home/user")
	p := paths.NewProject(g, "abc-123")
	if p.Dir != "/home/user/.shell3/projects/abc-123" {
		t.Fatalf("Dir: got %q", p.Dir)
	}
	if p.DB != "/home/user/.shell3/projects/abc-123/shell3.db" {
		t.Fatalf("DB: got %q", p.DB)
	}
	if p.Meta != "/home/user/.shell3/projects/abc-123/meta.json" {
		t.Fatalf("Meta: got %q", p.Meta)
	}
}

func TestLocal(t *testing.T) {
	l := paths.NewLocal("/work/project")
	if l.Root != "/work/project/.shell3" {
		t.Fatalf("Root: got %q", l.Root)
	}
	if l.Ref != "/work/project/.shell3/.ref" {
		t.Fatalf("Ref: got %q", l.Ref)
	}
	if l.Personas != "/work/project/.shell3/personas" {
		t.Fatalf("Personas: got %q", l.Personas)
	}
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
cd /path/to/shell3 && go test ./internal/paths/... 2>&1 | head -5
```
Expected: `no Go files` or `package not found`

- [ ] **Step 3: Write the implementation**

```go
// internal/paths/paths.go
package paths

import "path/filepath"

// Global holds all paths under ~/.shell3/ (user-scoped, never in repo).
type Global struct {
	Root        string // ~/.shell3/
	Credentials string // ~/.shell3/credentials.shell3
	Secrets     string // ~/.shell3/secrets.shell3
	Skills      string // ~/.shell3/skills/
	Tools       string // ~/.shell3/tools/
	Hooks       string // ~/.shell3/hooks/
	Personas    string // ~/.shell3/personas/
	Projects    string // ~/.shell3/projects/
}

// Project holds paths for one project's personal state keyed by UUID.
type Project struct {
	Dir  string // ~/.shell3/projects/<uuid>/
	DB   string // ~/.shell3/projects/<uuid>/shell3.db
	Meta string // ~/.shell3/projects/<uuid>/meta.json
}

// Local holds paths under ./.shell3/ (project-scoped, committed to repo).
type Local struct {
	Root     string // ./.shell3/
	Ref      string // ./.shell3/.ref  (gitignored)
	Skills   string // ./.shell3/skills/
	Tools    string // ./.shell3/tools/
	Hooks    string // ./.shell3/hooks/
	Personas string // ./.shell3/personas/
}

func NewGlobal(homeDir string) Global {
	root := filepath.Join(homeDir, ".shell3")
	return Global{
		Root:        root,
		Credentials: filepath.Join(root, "credentials.shell3"),
		Secrets:     filepath.Join(root, "secrets.shell3"),
		Skills:      filepath.Join(root, "skills"),
		Tools:       filepath.Join(root, "tools"),
		Hooks:       filepath.Join(root, "hooks"),
		Personas:    filepath.Join(root, "personas"),
		Projects:    filepath.Join(root, "projects"),
	}
}

func NewProject(g Global, uuid string) Project {
	dir := filepath.Join(g.Projects, uuid)
	return Project{
		Dir:  dir,
		DB:   filepath.Join(dir, "shell3.db"),
		Meta: filepath.Join(dir, "meta.json"),
	}
}

func NewLocal(cwd string) Local {
	root := filepath.Join(cwd, ".shell3")
	return Local{
		Root:     root,
		Ref:      filepath.Join(root, ".ref"),
		Skills:   filepath.Join(root, "skills"),
		Tools:    filepath.Join(root, "tools"),
		Hooks:    filepath.Join(root, "hooks"),
		Personas: filepath.Join(root, "personas"),
	}
}
```

- [ ] **Step 4: Run test — verify it passes**

```bash
go test ./internal/paths/... -v
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/paths/
git commit -m "feat(paths): centralize path construction in Paths structs"
```

---

## Task 2: `internal/obfile` — extract obfuscated file I/O

**Files:**
- Create: `internal/obfile/obfile.go`
- Create: `internal/obfile/obfile_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/obfile/obfile_test.go
package obfile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/obfile"
)

type testData struct {
	Keys map[string]string `yaml:"keys"`
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.store")

	want := testData{Keys: map[string]string{"foo": "bar", "baz": "qux"}}
	if err := obfile.Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// File must not contain plaintext keys.
	raw, _ := os.ReadFile(path)
	if contains(raw, "foo") || contains(raw, "bar") {
		t.Fatal("obfile wrote plaintext")
	}

	var got testData
	if err := obfile.Read(path, &got); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Keys["foo"] != "bar" || got.Keys["baz"] != "qux" {
		t.Fatalf("got %v", got.Keys)
	}
}

func TestReadMissing(t *testing.T) {
	var v testData
	err := obfile.Read("/nonexistent/path.store", &v)
	if err != nil {
		t.Fatalf("missing file should return nil, got: %v", err)
	}
}

func TestWriteCreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "store.s3")
	if err := obfile.Write(path, testData{Keys: map[string]string{"a": "b"}}); err != nil {
		t.Fatalf("Write nested: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func contains(b []byte, s string) bool {
	return len(b) > 0 && string(b) != "" && (func() bool {
		for i := range b {
			if i+len(s) <= len(b) && string(b[i:i+len(s)]) == s {
				return true
			}
		}
		return false
	})()
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
go test ./internal/obfile/... 2>&1 | head -5
```
Expected: `no Go files` or `package not found`

- [ ] **Step 3: Write the implementation**

```go
// internal/obfile/obfile.go
package obfile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/weatherjean/shell3/internal/config"
)

// Read decrypts and unmarshals the obfuscated YAML file at path into v.
// Returns nil (leaving v unchanged) if the file does not exist.
func Read(path string, v any) error {
	blob, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("obfile: read %s: %w", path, err)
	}
	plain, err := config.Unwrap(blob)
	if err != nil {
		return fmt.Errorf("obfile: unwrap %s: %w", path, err)
	}
	if err := yaml.Unmarshal(plain, v); err != nil {
		return fmt.Errorf("obfile: parse %s: %w", path, err)
	}
	return nil
}

// Write marshals v to YAML, obfuscates it, and writes atomically to path.
// Creates parent directories as needed (mode 0700).
func Write(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("obfile: mkdir: %w", err)
	}
	plain, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("obfile: marshal: %w", err)
	}
	wrapped := config.Wrap(plain)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, wrapped, 0600); err != nil {
		return fmt.Errorf("obfile: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("obfile: rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run test — verify it passes**

```bash
go test ./internal/obfile/... -v
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/obfile/
git commit -m "feat(obfile): extract obfuscated YAML file I/O into shared package"
```

---

## Task 3: Refactor `CredStore` to use `obfile`

**Files:**
- Modify: `internal/config/credstore.go`

- [ ] **Step 1: Run existing credstore tests — establish baseline**

```bash
go test ./internal/config/... -v
```
Expected: all PASS (note which tests exist)

- [ ] **Step 2: Rewrite `credstore.go` using `obfile`**

Replace the file content. The public API (types, method signatures) stays identical — only the internal load/save implementation changes.

```go
package config

import (
	"fmt"
	"sort"
	"sync"

	"github.com/weatherjean/shell3/internal/obfile"
)

type instanceRecord struct {
	Adapter string            `yaml:"adapter"`
	Fields  map[string]string `yaml:"fields"`
}

// credsFile is the on-disk root object. Version field removed — was always 1.
type credsFile struct {
	Instances map[string]instanceRecord `yaml:"instances"`
}

// InstanceMeta is the public summary of one configured instance.
type InstanceMeta struct {
	Instance string
	Adapter  string
}

// CredStore is the unified credential store backed by ~/.shell3/credentials.shell3.
type CredStore struct {
	path string

	mu   sync.Mutex
	data credsFile
}

// LoadCredStore reads ~/.shell3/credentials.shell3 if present.
func LoadCredStore(homeDir string) (*CredStore, error) {
	c := &CredStore{
		path: credsPath(homeDir),
		data: credsFile{Instances: map[string]instanceRecord{}},
	}
	if err := obfile.Read(c.path, &c.data); err != nil {
		return nil, fmt.Errorf("config: load credentials: %w", err)
	}
	if c.data.Instances == nil {
		c.data.Instances = map[string]instanceRecord{}
	}
	return c, nil
}

func credsPath(homeDir string) string {
	return paths.NewGlobal(homeDir).Credentials
}

func (c *CredStore) Set(instance, adapter string, fields map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make(map[string]string, len(fields))
	for k, v := range fields {
		cp[k] = v
	}
	c.data.Instances[instance] = instanceRecord{Adapter: adapter, Fields: cp}
	return c.saveLocked()
}

func (c *CredStore) Get(instance string) (adapter string, fields map[string]string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.data.Instances[instance]
	if !ok {
		return "", nil, false
	}
	out := make(map[string]string, len(rec.Fields))
	for k, v := range rec.Fields {
		out[k] = v
	}
	return rec.Adapter, out, true
}

func (c *CredStore) Update(instance string, fn func(fields map[string]string) error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.data.Instances[instance]
	if !ok {
		return fmt.Errorf("config: no instance %q", instance)
	}
	cp := make(map[string]string, len(rec.Fields))
	for k, v := range rec.Fields {
		cp[k] = v
	}
	if err := fn(cp); err != nil {
		return err
	}
	rec.Fields = cp
	c.data.Instances[instance] = rec
	return c.saveLocked()
}

func (c *CredStore) Delete(instance string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data.Instances, instance)
	return c.saveLocked()
}

func (c *CredStore) List() []InstanceMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]InstanceMeta, 0, len(c.data.Instances))
	for name, rec := range c.data.Instances {
		out = append(out, InstanceMeta{Instance: name, Adapter: rec.Adapter})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Instance < out[j].Instance })
	return out
}

// HomeDir returns the home directory derived from the store path.
// Kept for adapter compatibility.
func (c *CredStore) HomeDir() string {
	// path is ~/.shell3/credentials.shell3; walk up two levels
	return filepath.Dir(filepath.Dir(c.path))
}

func (c *CredStore) saveLocked() error {
	return obfile.Write(c.path, c.data)
}
```

Note: add `"path/filepath"` and `"github.com/weatherjean/shell3/internal/paths"` imports; remove old imports.

- [ ] **Step 3: Run tests — verify they pass**

```bash
go test ./internal/config/... -v
```
Expected: all PASS

- [ ] **Step 4: Build to catch compile errors**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 5: Commit**

```bash
git add internal/config/credstore.go
git commit -m "refactor(config): use obfile for CredStore load/save, drop Version field"
```

---

## Task 4: Refactor `secrets.Store` — use `obfile`, change to global path

**Files:**
- Modify: `internal/secrets/store.go`

The secrets store moves from `./.shell3/secrets.shell3` (project-scoped) to `~/.shell3/secrets.shell3` (global). The public API changes: `Load` now takes `homeDir` instead of `projectDir`.

- [ ] **Step 1: Run existing tests — establish baseline**

```bash
go test ./internal/secrets/... -v
```
Expected: all PASS (note which tests exist)

- [ ] **Step 2: Rewrite `store.go`**

```go
// Package secrets manages global user secrets stored at ~/.shell3/secrets.shell3.
// Secrets are exposed to user tools that declare the matching key in their
// tool YAML's "secrets:" field.
package secrets

import (
	"fmt"
	"sort"
	"sync"

	"github.com/weatherjean/shell3/internal/obfile"
	"github.com/weatherjean/shell3/internal/paths"
)

type secretsFile struct {
	Secrets map[string]string `yaml:"secrets"`
}

// Store is the global secrets store. Keys are environment-variable style names.
type Store struct {
	path string

	mu   sync.Mutex
	data secretsFile
}

// Load reads ~/.shell3/secrets.shell3. Returns an empty store if the file
// does not exist — first-use auto-creates on next Set.
func Load(homeDir string) (*Store, error) {
	g := paths.NewGlobal(homeDir)
	s := &Store{
		path: g.Secrets,
		data: secretsFile{Secrets: map[string]string{}},
	}
	if err := obfile.Read(s.path, &s.data); err != nil {
		return nil, fmt.Errorf("secrets: load: %w", err)
	}
	if s.data.Secrets == nil {
		s.data.Secrets = map[string]string{}
	}
	return s, nil
}

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

func (s *Store) All() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.data.Secrets))
	for k, v := range s.data.Secrets {
		out[k] = v
	}
	return out
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data.Secrets[key]
	return v, ok
}

func (s *Store) Set(key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Secrets[key] = value
	return obfile.Write(s.path, s.data)
}

func (s *Store) Remove(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Secrets, key)
	return obfile.Write(s.path, s.data)
}
```

- [ ] **Step 3: Update tests for global path**

In `internal/secrets/store_test.go`, replace any `secrets.Load(projectDir)` with `secrets.Load(homeDir)`. Tests should use a temp dir as `homeDir` and verify reads/writes go to `homeDir/.shell3/secrets.shell3`.

Example test shape:
```go
func TestSetGet(t *testing.T) {
	home := t.TempDir()
	s, err := secrets.Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Set("KEY", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, ok := s.Get("KEY")
	if !ok || v != "value" {
		t.Fatalf("Get: ok=%v v=%q", ok, v)
	}
	// Reload from disk — verify persistence.
	s2, _ := secrets.Load(home)
	if v2, _ := s2.Get("KEY"); v2 != "value" {
		t.Fatal("not persisted")
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/secrets/... -v
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/secrets/
git commit -m "refactor(secrets): move to global ~/.shell3/secrets.shell3, use obfile"
```

---

## Task 5: Update `secrets` CLI to use global path

**Files:**
- Modify: `cmd/shell3/secrets.go`

- [ ] **Step 1: Update all `secrets.Load(cwd)` calls to `secrets.Load(homeDir)`**

In `secrets.go`, every `RunE` currently does:
```go
cwd, err := os.Getwd()
s, err := secrets.Load(cwd)
```

Replace with:
```go
homeDir, err := os.UserHomeDir()
s, err := secrets.Load(homeDir)
```

Remove all `os.Getwd()` calls from `secrets.go`. Remove the `cwd` variable.

Update the command `Long` description: change `.shell3/secrets.shell3 (project-scoped)` → `~/.shell3/secrets.shell3 (global)`.

- [ ] **Step 2: Build**

```bash
go build ./cmd/shell3/
```
Expected: no errors

- [ ] **Step 3: Smoke test**

```bash
go run ./cmd/shell3/ secrets list
```
Expected: either "No secrets configured" or a list. No `shell3 init` error.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/secrets.go
git commit -m "feat(cli): secrets command now operates on global ~/.shell3/secrets.shell3"
```

---

## Task 6: `internal/ref` — project UUID ref file + meta.json

**Files:**
- Create: `internal/ref/ref.go`
- Create: `internal/ref/ref_test.go`

The `.ref` file lives at `.shell3/.ref` (one line: UUID). `meta.json` lives at `~/.shell3/projects/<uuid>/meta.json`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/ref/ref_test.go
package ref_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

func setup(t *testing.T) (homeDir, cwd string, g paths.Global, l paths.Local) {
	t.Helper()
	tmp := t.TempDir()
	homeDir = filepath.Join(tmp, "home")
	cwd = filepath.Join(tmp, "project")
	os.MkdirAll(filepath.Join(cwd, ".shell3"), 0755)
	g = paths.NewGlobal(homeDir)
	l = paths.NewLocal(cwd)
	return
}

func TestInitCreatesRefAndMeta(t *testing.T) {
	homeDir, cwd, g, l := setup(t)
	uuid, err := ref.Init(l, g, cwd)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if uuid == "" {
		t.Fatal("empty uuid")
	}

	// .ref file exists and contains the uuid
	loaded, err := ref.Load(l)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded != uuid {
		t.Fatalf("Load: got %q want %q", loaded, uuid)
	}

	// meta.json exists under projects dir
	p := paths.NewProject(g, uuid)
	if _, err := os.Stat(p.Meta); err != nil {
		t.Fatalf("meta.json missing: %v", err)
	}

	meta, err := ref.ReadMeta(p)
	if err != nil {
		t.Fatalf("ReadMeta: %v", err)
	}
	if meta.CWD != cwd {
		t.Fatalf("meta.CWD: got %q want %q", meta.CWD, cwd)
	}
	if meta.UUID != uuid {
		t.Fatalf("meta.UUID: got %q want %q", meta.UUID, uuid)
	}
	_ = homeDir
}

func TestLoadMissing(t *testing.T) {
	_, _, _, l := setup(t)
	uuid, err := ref.Load(l)
	if err != nil {
		t.Fatalf("Load missing: %v", err)
	}
	if uuid != "" {
		t.Fatalf("expected empty, got %q", uuid)
	}
}

func TestInitIdempotent(t *testing.T) {
	homeDir, cwd, g, l := setup(t)
	uuid1, _ := ref.Init(l, g, cwd)
	uuid2, _ := ref.Init(l, g, cwd)
	if uuid1 != uuid2 {
		t.Fatalf("Init not idempotent: %q vs %q", uuid1, uuid2)
	}
	_ = homeDir
}

func TestFindByCWD(t *testing.T) {
	homeDir, cwd, g, l := setup(t)
	uuid, _ := ref.Init(l, g, cwd)

	found, err := ref.FindByCWD(g, cwd)
	if err != nil {
		t.Fatalf("FindByCWD: %v", err)
	}
	if found != uuid {
		t.Fatalf("FindByCWD: got %q want %q", found, uuid)
	}
	_ = homeDir
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
go test ./internal/ref/... 2>&1 | head -5
```
Expected: `no Go files`

- [ ] **Step 3: Write the implementation**

```go
// internal/ref/ref.go
package ref

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/weatherjean/shell3/internal/paths"
)

// Meta is the content of ~/.shell3/projects/<uuid>/meta.json.
// It lets AIs (and humans) restore the .ref file if lost.
type Meta struct {
	UUID      string    `json:"uuid"`
	CWD       string    `json:"cwd"`
	Name      string    `json:"name"`       // basename of CWD
	CreatedAt time.Time `json:"created_at"`
}

// Load reads the UUID from l.Ref. Returns ("", nil) if the file is absent.
func Load(l paths.Local) (string, error) {
	b, err := os.ReadFile(l.Ref)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("ref: read: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// Init creates the .ref file and project dir if they don't exist.
// Idempotent: returns existing UUID if .ref already present.
func Init(l paths.Local, g paths.Global, cwd string) (string, error) {
	if id, err := Load(l); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}

	id := uuid.New().String()
	p := paths.NewProject(g, id)

	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return "", fmt.Errorf("ref: mkdir project dir: %w", err)
	}

	meta := Meta{
		UUID:      id,
		CWD:       cwd,
		Name:      filepath.Base(cwd),
		CreatedAt: time.Now().UTC(),
	}
	if err := writeMeta(p, meta); err != nil {
		return "", err
	}

	if err := os.WriteFile(l.Ref, []byte(id+"\n"), 0600); err != nil {
		return "", fmt.Errorf("ref: write .ref: %w", err)
	}
	return id, nil
}

// ReadMeta reads ~/.shell3/projects/<uuid>/meta.json.
func ReadMeta(p paths.Project) (Meta, error) {
	b, err := os.ReadFile(p.Meta)
	if err != nil {
		return Meta{}, fmt.Errorf("ref: read meta: %w", err)
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, fmt.Errorf("ref: parse meta: %w", err)
	}
	return m, nil
}

// FindByCWD scans ~/.shell3/projects/*/meta.json for a matching CWD.
// Returns ("", nil) if not found.
func FindByCWD(g paths.Global, cwd string) (string, error) {
	entries, err := os.ReadDir(g.Projects)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("ref: scan projects: %w", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := paths.NewProject(g, e.Name())
		m, err := ReadMeta(p)
		if err != nil {
			continue
		}
		if m.CWD == cwd {
			return m.UUID, nil
		}
	}
	return "", nil
}

func writeMeta(p paths.Project, m Meta) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("ref: marshal meta: %w", err)
	}
	return os.WriteFile(p.Meta, b, 0600)
}
```

- [ ] **Step 4: Promote `google/uuid` to direct dependency**

```bash
go get github.com/google/uuid
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/ref/... -v
```
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/ref/ go.mod go.sum
git commit -m "feat(ref): project UUID ref file and meta.json for personal state bridging"
```

---

## Task 7: Auto-bootstrap on first run

**Files:**
- Create: `internal/bootstrap/bootstrap.go`
- Create: `internal/bootstrap/bootstrap_test.go`
- Modify: `cmd/shell3/run.go`

Bootstrap ensures `~/.shell3/` dirs exist and creates project `.ref` + dirs on first run. It also writes `.shell3/.ref` to `.gitignore` if needed.

- [ ] **Step 1: Write the failing tests**

```go
// internal/bootstrap/bootstrap_test.go
package bootstrap_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

func TestEnsureGlobal(t *testing.T) {
	home := t.TempDir()
	g := paths.NewGlobal(home)
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	for _, dir := range []string{g.Skills, g.Tools, g.Hooks, g.Personas, g.Projects} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("dir missing: %s", dir)
		}
	}
}

func TestEnsureProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	os.MkdirAll(cwd, 0755)

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)

	bootstrap.EnsureGlobal(g)
	id, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if id == "" {
		t.Fatal("empty uuid")
	}

	// .shell3/ dirs created
	for _, dir := range []string{l.Skills, l.Tools, l.Hooks, l.Personas} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("local dir missing: %s", dir)
		}
	}

	// .ref file present
	loaded, _ := ref.Load(l)
	if loaded != id {
		t.Fatalf("ref mismatch: %q vs %q", loaded, id)
	}

	// .gitignore contains .ref
	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if !contains(string(gi), ".ref") {
		t.Fatal(".gitignore missing .ref entry")
	}
}

func TestEnsureProjectIdempotent(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	os.MkdirAll(cwd, 0755)
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)

	id1, _ := bootstrap.EnsureProject(l, g, cwd)
	id2, _ := bootstrap.EnsureProject(l, g, cwd)
	if id1 != id2 {
		t.Fatalf("not idempotent: %q vs %q", id1, id2)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := range s {
		if i+len(sub) <= len(s) && s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
go test ./internal/bootstrap/... 2>&1 | head -5
```
Expected: `no Go files`

- [ ] **Step 3: Write the implementation**

```go
// internal/bootstrap/bootstrap.go
package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

// EnsureGlobal creates ~/.shell3/ and its subdirectories if missing.
func EnsureGlobal(g paths.Global) error {
	for _, dir := range []string{
		g.Root, g.Skills, g.Tools, g.Hooks, g.Personas, g.Projects,
	} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// EnsureProject creates .shell3/ subdirectories and the .ref file for this
// project. Returns the project UUID (creating one on first call).
func EnsureProject(l paths.Local, g paths.Global, cwd string) (string, error) {
	for _, dir := range []string{
		l.Root, l.Skills, l.Tools, l.Hooks, l.Personas,
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return "", fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
		}
	}

	if err := ensureGitignore(l); err != nil {
		return "", err
	}

	id, err := ref.Init(l, g, cwd)
	if err != nil {
		return "", fmt.Errorf("bootstrap: ref init: %w", err)
	}
	p := paths.NewProject(g, id)
	if err := os.MkdirAll(p.Dir, 0700); err != nil {
		return "", fmt.Errorf("bootstrap: mkdir project dir: %w", err)
	}
	return id, nil
}

const gitignorePath = ".gitignore"

func ensureGitignore(l paths.Local) error {
	path := filepath.Join(l.Root, gitignorePath)
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: read gitignore: %w", err)
	}
	if strings.Contains(string(b), ".ref") {
		return nil
	}
	entry := "\n.ref\n"
	if len(b) == 0 {
		entry = ".ref\n"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("bootstrap: open gitignore: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(entry)
	return err
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/bootstrap/... -v
```
Expected: all PASS

- [ ] **Step 5: Wire bootstrap into `run.go`**

In `cmd/shell3/run.go`, at the top of `RunE`, replace the existing `cwd`/`homeDir` setup with:

```go
cwd, err := os.Getwd()
if err != nil {
    return fmt.Errorf("get cwd: %w", err)
}
homeDir, err := os.UserHomeDir()
if err != nil {
    return fmt.Errorf("get home dir: %w", err)
}
g := paths.NewGlobal(homeDir)
l := paths.NewLocal(cwd)

if err := bootstrap.EnsureGlobal(g); err != nil {
    return err
}
uuid, err := bootstrap.EnsureProject(l, g, cwd)
if err != nil {
    return err
}
proj := paths.NewProject(g, uuid)
```

Then replace scattered `filepath.Join(cwd, ".shell3/...")` and `filepath.Join(homeDir, ".shell3/...")` with the struct fields:
- `filepath.Join(cwd, ".shell3/personas")` → `l.Personas`
- `filepath.Join(homeDir, ".shell3", "tools")` → `g.Tools`
- `filepath.Join(cwd, ".shell3", "tools")` → `l.Tools`
- `storeDBPath` → `proj.DB`
- `secrets.Load(cwd)` → `secrets.Load(homeDir)`

- [ ] **Step 6: Build**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 7: Commit**

```bash
git add internal/bootstrap/ cmd/shell3/run.go
git commit -m "feat(bootstrap): auto-create ~/.shell3/ and project .ref on first run"
```

---

## Task 8: Drop `init` command

**Files:**
- Delete: `cmd/shell3/init.go`
- Modify: `cmd/shell3/main.go`
- Modify: `internal/scaffold/scaffold.go`

- [ ] **Step 1: Delete `init.go`**

```bash
rm cmd/shell3/init.go
```

- [ ] **Step 2: Remove init from `main.go`**

Remove the line:
```go
root.AddCommand(newInitCommand())
```

Remove the `"github.com/weatherjean/shell3/internal/scaffold"` and `"github.com/weatherjean/shell3/internal/llm"` imports from `main.go` if they're now unused (they likely were only used by init).

- [ ] **Step 3: Simplify `scaffold.go`**

The `InitProject` and `checkCredentials` functions are now dead. Remove them. Keep only `initShell3Dir` logic, but repurpose it as a helper `WriteDefaultPersona(personasDir string) error` — called by bootstrap when no persona exists yet.

Replace `scaffold.go` content:

```go
package scaffold

import (
	"os"
	"path/filepath"
)

const DefaultPersonaName = "code"

const codePersonaTemplate = `---
name: code
description: Agentic coding assistant with bash and memory tools
model: ~
provider: ~
no_bash: false
no_memory: false
parameters:
  reasoning_effort: ~
  reasoning_summary: ~
  verbosity: ~
  parallel_tool_calls: ~
  temperature: ~
---
{{- if .CoreMemories}}
## Core memories
{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}{{end}}
## Context
- CWD: {{.CWD}}
- Time: {{.Time}}
- Model: {{.Model}}

## Skills
{{.Skills}}

You are a coding agent. Use bash to explore and edit. Store important project facts with memory_upsert. Prefer targeted edits over full rewrites.
`

const braveSearchTool = `name: brave_search
description: Web search via the Brave Search API. Returns top results as JSON. Set enabled to true after running 'shell3 secrets set --key BRAVE_API_KEY --secret <token>'.
enabled: false
secrets:
  - BRAVE_API_KEY
parameters:
  type: object
  properties:
    query:
      type: string
      description: Search query
    count:
      type: integer
      description: Result count (1-20)
      default: 5
  required: [query]
command: |
  curl -sG https://api.search.brave.com/res/v1/web/search \
    -H "X-Subscription-Token: $BRAVE_API_KEY" \
    -H "Accept: application/json" \
    --data-urlencode "q=$QUERY" \
    --data-urlencode "count=${COUNT:-5}"
timeout: 15s
`

// WriteDefaults writes the default persona and example tool if they don't
// exist. Safe to call on every run — skips files that are already present.
func WriteDefaults(personasDir, toolsDir string) error {
	personaPath := filepath.Join(personasDir, DefaultPersonaName+".md")
	if err := writeIfAbsent(personaPath, codePersonaTemplate, 0644); err != nil {
		return err
	}
	toolPath := filepath.Join(toolsDir, "brave_search.yaml")
	return writeIfAbsent(toolPath, braveSearchTool, 0644)
}

func writeIfAbsent(path, content string, perm os.FileMode) error {
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	}
	return os.WriteFile(path, []byte(content), perm)
}
```

- [ ] **Step 4: Call `scaffold.WriteDefaults` from bootstrap**

In `bootstrap.go`, after creating local dirs, add:
```go
if err := scaffold.WriteDefaults(l.Personas, l.Tools); err != nil {
    return "", fmt.Errorf("bootstrap: write defaults: %w", err)
}
```

Add `"github.com/weatherjean/shell3/internal/scaffold"` to bootstrap imports.

- [ ] **Step 5: Build and verify**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add cmd/shell3/main.go internal/scaffold/scaffold.go internal/bootstrap/bootstrap.go
git rm cmd/shell3/init.go
git commit -m "feat: replace init command with auto-bootstrap on first run"
```

---

## Task 9: Fix persona default name

**Files:**
- Modify: `cmd/shell3/run.go`

The `--persona` flag currently defaults to `"base"`. The default persona file is now `code.md` (written by scaffold in Task 8). Fix the flag.

- [ ] **Step 1: Find the flag definition in `run.go`**

Look for: `"persona", "base"` — the flag default.

- [ ] **Step 2: Change default**

```go
// before
cmd.Flags().StringVar(&personaName, "persona", "base", "Persona to load from .shell3/personas/")

// after
cmd.Flags().StringVar(&personaName, "persona", scaffold.DefaultPersonaName, "Persona to load from .shell3/personas/")
```

Add `"github.com/weatherjean/shell3/internal/scaffold"` to `run.go` imports if not already present.

Also update the error message in `persona.go` that says `"run: shell3 init"` → `"check .shell3/personas/ or run shell3 doctor"`.

- [ ] **Step 3: Build**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/run.go internal/persona/persona.go
git commit -m "fix(persona): default to 'code' persona, update stale error messages"
```

---

## Task 10: `cmd/shell3/doctor.go` — validate setup

**Files:**
- Create: `cmd/shell3/doctor.go`
- Modify: `cmd/shell3/main.go`

- [ ] **Step 1: Write the tests**

Create `cmd/shell3/doctor_test.go`:

```go
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorAllGreen(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	// Set up minimal passing state: global dirs + creds + project .ref
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)
	bootstrap.EnsureProject(l, g, cwd)

	// Write a dummy credential
	store, _ := config.LoadCredStore(home)
	store.Set("test", "openai", map[string]string{"api_key": "sk-test"})

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput:\n%s", code, out.String())
	}
	if !containsStr(out.String(), "credentials") {
		t.Error("output missing credentials check")
	}
}

func TestDoctorMissingCredentials(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)
	bootstrap.EnsureProject(l, g, cwd)
	// No credentials set

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code == 0 {
		t.Fatal("expected non-zero exit, got 0")
	}
	if !containsStr(out.String(), "no credentials") {
		t.Errorf("expected 'no credentials' in output, got:\n%s", out.String())
	}
}

func TestDoctorMissingRef(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	bootstrap.EnsureGlobal(g)
	// No project bootstrap — no .ref file

	var out bytes.Buffer
	code := runDoctor(home, cwd, &out)
	if code == 0 {
		t.Fatal("expected non-zero exit for missing .ref")
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && bytes.Contains([]byte(s), []byte(sub))
}
```

- [ ] **Step 2: Run test — verify it fails**

```bash
go test ./cmd/shell3/... -run TestDoctor 2>&1 | head -10
```
Expected: `undefined: runDoctor`

- [ ] **Step 3: Write the implementation**

```go
// cmd/shell3/doctor.go
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
	"github.com/weatherjean/shell3/internal/scaffold"
	"github.com/weatherjean/shell3/internal/secrets"
)

func newDoctorCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate shell3 setup",
		Long:  `Check global and project configuration. Exit 0 if all checks pass.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			code := runDoctor(homeDir, cwd, cmd.OutOrStdout())
			if code != 0 {
				os.Exit(code)
			}
			return nil
		},
	}
}

func runDoctor(homeDir, cwd string, out io.Writer) int {
	g := paths.NewGlobal(homeDir)
	l := paths.NewLocal(cwd)
	failures := 0
	fail := func() { failures++ }

	fmt.Fprintln(out, "Global")
	checkGlobal(out, g, fail)

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Project")
	checkProject(out, l, g, cwd, fail)

	if failures > 0 {
		fmt.Fprintf(out, "\n%d check(s) failed.\n", failures)
		return 1
	}
	fmt.Fprintln(out, "\nAll checks passed.")
	return 0
}

func checkGlobal(out io.Writer, g paths.Global, fail func()) {
	check(out, fail, dirExists(g.Root), "~/.shell3/ exists")
	check(out, fail, dirExists(g.Skills), "global skills dir")
	check(out, fail, dirExists(g.Tools), "global tools dir")

	credStore, err := config.LoadCredStore(g.Root[:len(g.Root)-len("/.shell3")])
	if err == nil && len(credStore.List()) > 0 {
		names := make([]string, 0)
		for _, m := range credStore.List() {
			names = append(names, m.Instance)
		}
		check(out, fail, true, fmt.Sprintf("credentials: %v", names))
	} else {
		check(out, fail, false, "no credentials — run: shell3 auth")
	}

	secStore, err := secrets.Load(g.Root[:len(g.Root)-len("/.shell3")])
	check(out, fail, err == nil, "secrets store accessible")
	_ = secStore
}

func checkProject(out io.Writer, l paths.Local, g paths.Global, cwd string, fail func()) {
	check(out, fail, dirExists(l.Root), ".shell3/ exists")

	uuid, err := ref.Load(l)
	if err != nil || uuid == "" {
		check(out, fail, false, ".ref missing — run shell3 in this directory to bootstrap")
		return
	}
	p := paths.NewProject(g, uuid)
	check(out, fail, true, fmt.Sprintf(".ref → ~/.shell3/projects/%s/", uuid))

	meta, err := ref.ReadMeta(p)
	if err == nil {
		check(out, fail, meta.CWD == cwd, fmt.Sprintf("meta.json: project=%s", meta.Name))
	} else {
		check(out, fail, false, "meta.json unreadable")
	}

	_, dbErr := os.Stat(p.DB)
	check(out, fail, dbErr == nil, "shell3.db accessible")

	personaPath := l.Personas + "/" + scaffold.DefaultPersonaName + ".md"
	check(out, fail, fileExists(personaPath), fmt.Sprintf("persona: %s", scaffold.DefaultPersonaName))
}

func check(out io.Writer, fail func(), ok bool, msg string) {
	if ok {
		fmt.Fprintf(out, "  ✓ %s\n", msg)
	} else {
		fmt.Fprintf(out, "  ✗ %s\n", msg)
		fail()
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
```

- [ ] **Step 4: Register in `main.go`**

Add `root.AddCommand(newDoctorCommand())` in `main.go`.

- [ ] **Step 5: Run tests**

```bash
go test ./cmd/shell3/... -run TestDoctor -v
```
Expected: all PASS

- [ ] **Step 6: Build and smoke test**

```bash
go build ./cmd/shell3/ && ./shell3 doctor
```
Expected: output with check marks

- [ ] **Step 7: Commit**

```bash
git add cmd/shell3/doctor.go cmd/shell3/doctor_test.go cmd/shell3/main.go
git commit -m "feat(cli): add doctor command to validate global and project setup"
```

---

## Task 11: Consolidate hook dispatch functions

**Files:**
- Modify: `internal/hooks/hooks.go`
- Modify: `internal/hooks/hooks_test.go`

The four functions `callHook`, `callHookTTYBlocking`, `callHookTTY`, `callHookSilent` all follow the same pattern. Consolidate into one private function.

- [ ] **Step 1: Run existing tests**

```bash
go test ./internal/hooks/... -v
```
Expected: all PASS (baseline)

- [ ] **Step 2: Add enum and consolidate in `hooks.go`**

Inside `hooks.go`, add the enum and replace the four functions:

```go
type dispatchMode int

const (
	modeBlocking       dispatchMode = iota // wait, capture stdout, no TTY
	modeTTYBlocking                        // wait, capture stdout, release TTY
	modeFireForgetTTY                      // no wait, inherit stdio, release TTY
	modeFireForgetSilent                   // no wait, discard output
)

func (r *Runner) dispatch(ctx context.Context, cmd string, input hookInput, mode dispatchMode) (hookOutput, error) {
	timeout := hookTimeout
	if mode == modeTTYBlocking || mode == modeFireForgetTTY {
		timeout = hookTTYTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if (mode == modeTTYBlocking || mode == modeFireForgetTTY) && r.releaser != nil {
		_ = r.releaser.Pause()
		defer r.releaser.Resume()
	}

	data, _ := json.Marshal(input)
	parts := strings.Fields(cmd)
	c := exec.CommandContext(ctx, parts[0], parts[1:]...)
	c.Stdin = bytes.NewReader(data)

	var stdout bytes.Buffer
	switch mode {
	case modeBlocking, modeTTYBlocking:
		c.Stdout = &stdout
		c.Stderr = os.Stderr
	case modeFireForgetTTY:
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	case modeFireForgetSilent:
		// discard
	}

	if err := c.Run(); err != nil {
		if mode == modeFireForgetTTY || mode == modeFireForgetSilent {
			return hookOutput{}, nil // fire-and-forget: ignore errors
		}
		return hookOutput{}, fmt.Errorf("hooks: %q failed: %w", cmd, err)
	}

	if mode == modeFireForgetTTY || mode == modeFireForgetSilent {
		return hookOutput{}, nil
	}
	if stdout.Len() == 0 {
		return hookOutput{Action: "allow"}, nil
	}
	var out hookOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return hookOutput{}, fmt.Errorf("hooks: %q bad JSON output: %w", cmd, err)
	}
	return out, nil
}
```

Then replace all call sites of `callHook`, `callHookTTYBlocking`, `callHookTTY`, `callHookSilent` with `r.dispatch(ctx, cmd, input, modeXxx)`.

Delete the four old functions.

- [ ] **Step 3: Run tests**

```bash
go test ./internal/hooks/... -v
```
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/hooks/hooks.go
git commit -m "refactor(hooks): consolidate 4 dispatch functions into one with mode enum"
```

---

## Task 12: Fix persona double file-read

**Files:**
- Modify: `internal/persona/persona.go`

`ParseConfig` reads and parses the file; `Load` reads and parses it again. Fix: `ParseConfig` returns the raw body alongside config; `Load` accepts `(cfg PersonaConfig, body string, ...)`.

- [ ] **Step 1: Run existing tests**

```bash
go test ./internal/persona/... -v
```
Expected: all PASS (baseline)

- [ ] **Step 2: Change `ParseConfig` signature**

```go
// ParseConfig reads <personasDir>/<name>.md, parses frontmatter, and returns
// both the config and the raw template body. Callers can pass the body
// directly to Load to avoid reading the file twice.
func ParseConfig(personasDir, name string) (PersonaConfig, string, error) {
	path := filepath.Join(personasDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PersonaConfig{}, "", fmt.Errorf("persona %q not found in %s — check .shell3/personas/ or run shell3 doctor", name, personasDir)
		}
		return PersonaConfig{}, "", fmt.Errorf("persona: read %s: %w", path, err)
	}
	fm, body := extractParts(string(raw))
	var cfg PersonaConfig
	if err := yaml.Unmarshal([]byte(fm), &cfg); err != nil {
		return PersonaConfig{}, "", fmt.Errorf("persona: parse frontmatter %s: %w", name, err)
	}
	if cfg.Name == "" {
		cfg.Name = name
	}
	return cfg, body, nil
}
```

- [ ] **Step 3: Change `Load` to accept pre-parsed config + body**

```go
// Load renders a persona given a pre-parsed config and template body.
// Obtain cfg and body from ParseConfig to avoid reading the file twice.
func Load(cfg PersonaConfig, body string, data TemplateData, hasStore, noBash bool, userTools []ToolDef) (Persona, error) {
	tmpl, err := template.New(cfg.Name).Parse(body)
	if err != nil {
		return Persona{}, fmt.Errorf("persona: parse template %s: %w", cfg.Name, err)
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return Persona{}, fmt.Errorf("persona: render %s: %w", cfg.Name, err)
	}
	var tools []ToolDef
	tools = append(tools, docsTool, pruneToolResultTool)
	if !noBash {
		tools = append(tools, bashTool, shellInteractiveTool, editFileTool, writeFileTool)
	}
	if hasStore {
		tools = append(tools, storeTools...)
	}
	tools = append(tools, userTools...)
	return Persona{
		Config:       cfg,
		Name:         cfg.Name,
		SystemPrompt: buf.String(),
		Tools:        tools,
		Parameters:   cfg.Parameters.ToRequestParams(),
	}, nil
}
```

- [ ] **Step 4: Update `run.go` call sites**

In `run.go`, replace:
```go
pCfg, err := persona.ParseConfig(personasDir, personaName)
// ...
pers, err := persona.Load(personasDir, personaName, personaData, st != nil, noBash, userToolDefs)
```
With:
```go
pCfg, personaBody, err := persona.ParseConfig(l.Personas, personaName)
// ...
pers, err := persona.Load(pCfg, personaBody, personaData, st != nil, noBash, userToolDefs)
```

- [ ] **Step 5: Run all tests**

```bash
go test ./internal/persona/... ./cmd/shell3/... -v
```
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add internal/persona/persona.go cmd/shell3/run.go
git commit -m "refactor(persona): ParseConfig returns body to avoid double file-read in Load"
```

---

## Task 13: Usertools cleanup — drop `Before`/`After`, fix reserved names

**Files:**
- Modify: `internal/usertools/usertools.go`
- Modify: `internal/persona/persona.go` (export tool name registry)

The `reservedNames` list is hardcoded and has wrong names. Derive it from `persona.go`'s actual tool definitions. Also remove the unused `Before` and `After` fields from `Spec`.

- [ ] **Step 1: Run existing tests**

```bash
go test ./internal/usertools/... -v
```
Expected: all PASS (baseline)

- [ ] **Step 2: Export built-in tool names from `persona.go`**

Add to `persona.go`:

```go
// BuiltinToolNames returns the names of all built-in tools. Used by usertools
// to prevent name collisions.
func BuiltinToolNames() map[string]struct{} {
	all := []ToolDef{docsTool, pruneToolResultTool, bashTool, shellInteractiveTool,
		editFileTool, writeFileTool}
	all = append(all, storeTools...)
	names := make(map[string]struct{}, len(all))
	for _, t := range all {
		names[t.Name] = struct{}{}
	}
	return names
}
```

- [ ] **Step 3: Update `usertools.go`**

Remove `Before` and `After` from `Spec`:
```go
type Spec struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Enabled     bool           `yaml:"enabled"`
	Parameters  map[string]any `yaml:"parameters"`
	Command     string         `yaml:"command"`
	Secrets     []string       `yaml:"secrets,omitempty"`
	Timeout     time.Duration  `yaml:"timeout,omitempty"`
	Cwd         string         `yaml:"cwd,omitempty"`
}
```

Replace the hardcoded `reservedNames` map:
```go
// reservedNames is derived from persona.BuiltinToolNames() at init time.
var reservedNames = persona.BuiltinToolNames()
```

Add import: `"github.com/weatherjean/shell3/internal/persona"`

Remove the old `var reservedNames = map[string]struct{}{...}` block.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/usertools/... -v
```
Expected: all PASS

- [ ] **Step 5: Build**

```bash
go build ./...
```
Expected: no errors

- [ ] **Step 6: Commit**

```bash
git add internal/usertools/usertools.go internal/persona/persona.go
git commit -m "refactor(usertools): drop unused Before/After, derive reserved names from persona registry"
```

---

## Task 14: Dead code and gitignore cleanup

**Files:**
- Modify: `internal/scaffold/scaffold.go` (gitignore constant)
- Modify: `.shell3/.gitignore`
- Modify: `internal/config/migrate.go` (if plans/ mentioned)

- [ ] **Step 1: Update the `defaultGitignore` constant in `scaffold.go`**

The new `.shell3/.gitignore` only needs one entry — `.ref`. DB, secrets, and last_* all live in `~/.shell3/projects/<uuid>/` now.

```go
const defaultGitignore = `.ref
`
```

- [ ] **Step 2: Update `.shell3/.gitignore` in this repo**

```bash
cat > .shell3/.gitignore << 'EOF'
.ref
EOF
```

Verify this doesn't accidentally un-gitignore `shell3.db` (it should already be in root `.gitignore` or not present if DB moved to home dir).

- [ ] **Step 3: Check `migrate.go` for any `plans/` or dead references**

```bash
grep -n "plans\|memory\.db\|history\.md" internal/config/migrate.go internal/scaffold/scaffold.go 2>/dev/null
```

Remove any references to `plans/`, `memory.db`, `history.md` from scaffold and docs.

- [ ] **Step 4: Build and run full test suite**

```bash
go build ./... && go test ./... -v 2>&1 | tail -30
```
Expected: all PASS, no errors

- [ ] **Step 5: Final smoke test**

```bash
# In a temp directory:
mkdir /tmp/shell3-test && cd /tmp/shell3-test
/path/to/shell3 doctor
```
Expected: shows checks, no panic

- [ ] **Step 6: Commit**

```bash
git add .shell3/.gitignore internal/scaffold/scaffold.go
git commit -m "chore: simplify .gitignore to .ref-only, remove dead plans/ and legacy entries"
```

---

## Self-Review

**Spec coverage check:**

| Requirement | Task |
|-------------|------|
| `Paths` struct | Task 1 |
| `obfile` unifies duplicate store code | Task 2 |
| CredStore uses obfile | Task 3 |
| Secrets global, uses obfile | Task 4 |
| secrets CLI → global | Task 5 |
| `.ref` + `meta.json` with cwd | Task 6 |
| Auto-bootstrap on first run | Task 7 |
| DB in `~/.shell3/projects/<uuid>/` | Task 7 |
| Drop `init` command | Task 8 |
| Default persona `code` | Task 9 |
| `doctor` command | Task 10 |
| Hook dispatch consolidation | Task 11 |
| Persona double-read fix | Task 12 |
| Drop `Before`/`After` from usertools | Task 13 |
| Fix reserved names from registry | Task 13 |
| Gitignore + dead code cleanup | Task 14 |

**Placeholder scan:** None found. All tasks have concrete code.

**Type consistency check:**
- `paths.Global`, `paths.Project`, `paths.Local` used consistently across tasks 1–10
- `ref.Init(l paths.Local, g paths.Global, cwd string)` matches usage in bootstrap Task 7
- `persona.ParseConfig` returns `(PersonaConfig, string, error)` — matches Task 12 call site in run.go
- `secrets.Load(homeDir string)` — matches Task 4 definition and Task 5 call site
- `bootstrap.EnsureProject(l, g, cwd)` — matches doctor test in Task 10
- `scaffold.WriteDefaults(personasDir, toolsDir string)` — matches bootstrap call in Task 8
