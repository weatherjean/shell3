# Adapter Refactor + Unified Obfuscated Credentials Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize every LLM backend into self-contained adapters under `internal/adapters/<name>`, collapse the dual code paths in `cmd/shell3/run.go` so all backends route through `llm.Provider`, unify all credential storage into a single XOR-obfuscated file (`~/.shell3/credentials.shell3`) with auto-migration from the legacy `credentials.yaml` and `codex_tokens.json`.

**Architecture:** `internal/llm/` shrinks to interfaces + value types only. Two adapter packages (`internal/adapters/openai`, `internal/adapters/codex`) self-register via `init()`. Each adapter owns its `Auth` UX (interactive prompts vs OAuth browser flow) and its `NewClient` constructor. A new `internal/config` `CredStore` is the single read/write point for credentials; both adapters use it. Storage format: YAML body wrapped by an XOR+base64 obfuscation layer (honest framing — defends against LLM accidental disclosure, not encryption). `cmd/shell3/run.go` dispatches uniformly via `llm.Get(name)`. `cmd/shell3/auth.go` with no `--provider` flag presents an adapter menu. The optional `TrafficInspector` interface preserves `/dump` raw-traffic capture without forcing all adapters to implement it.

**Tech Stack:** Go (existing module), `gopkg.in/yaml.v3`, stdlib `crypto/sha256`, `encoding/base64`, `os`, `path/filepath`. No new third-party deps.

---

## File Structure

**Create:**
- `internal/config/obfuscate.go` — `Wrap([]byte) []byte` and `Unwrap([]byte) ([]byte, error)`. XOR with SHA-256-derived repeating key, base64-encoded body, fixed magic header line.
- `internal/config/obfuscate_test.go` — round-trip tests, header sanity, tampered-input rejection.
- `internal/config/credstore.go` — `CredStore` type, `Load(homeDir)`, `(c *CredStore) Save()`, `(c *CredStore) Update(instance, fn)`, atomic write (tmp + rename) with 0600 perms, optional file lock for concurrent codex token refresh.
- `internal/config/credstore_test.go` — set/get/list/delete/update round-trip, atomic-write-on-corruption tests.
- `internal/config/migrate.go` — one-shot import from legacy `credentials.yaml` + `codex_tokens.json`. Idempotent.
- `internal/config/migrate_test.go` — migration golden tests.
- `internal/adapters/openai/client.go` — moved from `internal/llm/client.go`. The OpenAI-compatible streaming client + `bodyTap` debug capture. Preserves `LastTraffic`/`LastReasoning`/`SetModel`.
- `internal/adapters/openai/client_test.go` — moved from `internal/llm/client_test.go`.
- `internal/adapters/openai/auth.go` — `(*provider).Auth`: interactive prompts for `base_url`, `api_key`, `default_model`. Persists via `CredStore`.
- `internal/adapters/openai/register.go` — `package openai`, `init()` calls `llm.Register("openai", &provider{})`. `(*provider).NewClient` reads its instance config from `CredStore` and constructs `*Client`. `(*provider).Models(store, instance)` reports the configured `default_model` (split on `,` for multi-model lists). `(*provider).SingleInstance() bool { return false }`.
- `internal/adapters/codex/...` — renamed from `internal/providers/codex/`. All file contents preserved, only the import path changes plus token persistence is swapped from `tokens.go`'s direct file I/O to `CredStore`.

**Modify:**
- `internal/llm/provider.go` — extend `Provider` interface (`Name()`, `SingleInstance()`, `Auth(ctx, w, store, instance)`, `NewClient(ctx, store, instance)`, `Models(store, instance)`). Keep registry semantics.
- `internal/llm/types.go` — no change (left as a marker; this is the contract file).
- `internal/llm/client.go` — **delete** (moved). Removing breaks compile of `cmd/shell3/run.go` until the run.go updates land in the same task.
- `internal/llm/client_test.go` — **delete** (moved).
- `internal/config/credentials.go` — slimmed; `LoadCredentials` becomes a thin compatibility shim that calls `CredStore.Load` and projects `openai`-adapter instances back into the old `*Credentials` shape only for the brief migration window. Removed once `cmd/shell3/run.go` no longer reads it.
- `internal/config/auth.go` — **delete** `RunAuthInteractive` (moved into `internal/adapters/openai/auth.go`).
- `internal/config/credentials_test.go`, `internal/config/auth_test.go` — tests of moved code follow the code or are replaced by `credstore_test.go`.
- `internal/providers/codex/` — **directory removed** after the move.
- `internal/providers/codex/tokens.go` — `LoadTokens`/`SaveTokens` rewritten to read/write a `codex` instance via `CredStore`. The `Tokens` struct stays put; only persistence changes. After the move, lives at `internal/adapters/codex/tokens.go`.
- `internal/providers/codex/register.go` — moves to `internal/adapters/codex/register.go` and is updated to the new `Provider` interface signature.
- `cmd/shell3/run.go` — collapse the registry-vs-credentials bifurcation. Every backend lookup goes through `llm.Get`. The `*llm.Client` type assertions for in-place `SetModel` are replaced by the optional `ModelSetter` interface check.
- `cmd/shell3/auth.go` — no `--provider`: list registered adapters, prompt the user to choose, dispatch to that adapter's `Auth`. With `--provider=X`: same as today.
- `cmd/shell3/main.go` — blank-import both adapter packages.
- `cmd/shell3/init.go` — extend output to mention the configured-adapters list (so `shell3 init` is a discoverability surface for adapters).
- `cmd/shell3/shell3.md` — rewrite the Credentials section to describe the obfuscation honestly and document the new file path. Add an Adapters section listing the built-in adapters and how to add more.
- `internal/chat/turn.go` — already uses an interface-assertion `trafficSource`; rename to `TrafficInspector`, lift it to the public `internal/llm` package so adapters can implement it without importing `chat`.

---

## Conventions Used Below

- Module path: `github.com/weatherjean/shell3`
- All commits use Conventional Commits format (matches existing history).
- Tests use `t.TempDir()` for fixtures and Go's standard `testing` package.
- The XOR obfuscation key is derived as `sha256("shell3-creds-obfuscation-v1")`. **This is documented honestly** as obfuscation, not encryption — defending against LLM accidental disclosure only.
- The on-disk format magic header line is a literal `# shell3-obfuscated-v1 — not encrypted; do not paste contents` followed by `\n` then the base64 body. Header is plain text for human discoverability.
- File path: `~/.shell3/credentials.shell3`, mode `0600`, parent dir mode `0700`.
- Codex always lives at instance name `codex`. Re-running `shell3 auth --provider=codex` overwrites it.
- Multi-instance OpenAI-compat: instance name is user-chosen at `auth` time (e.g. `ollama-local`, `openrouter-prod`). Adapter name is always `openai`.

---

## Task 1: Add XOR obfuscation helper

**Files:**
- Create: `internal/config/obfuscate.go`
- Test: `internal/config/obfuscate_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/obfuscate_test.go
package config

import (
	"bytes"
	"strings"
	"testing"
)

func TestWrapUnwrap_RoundTrip(t *testing.T) {
	plain := []byte("version: 1\ninstances:\n  openai:\n    adapter: openai\n    fields:\n      api_key: sk-test\n")
	wrapped := Wrap(plain)
	if !bytes.HasPrefix(wrapped, []byte(obfuscateHeader)) {
		t.Fatalf("missing magic header: %q", wrapped[:min(len(wrapped), 64)])
	}
	if bytes.Contains(wrapped, []byte("sk-test")) {
		t.Fatalf("wrapped contained plaintext secret")
	}
	got, err := Unwrap(wrapped)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("round-trip mismatch:\n got:  %q\n want: %q", got, plain)
	}
}

func TestUnwrap_RejectsMissingHeader(t *testing.T) {
	_, err := Unwrap([]byte("not the magic header\nABCDEF=="))
	if err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("want header error, got %v", err)
	}
}

func TestUnwrap_RejectsCorruptBase64(t *testing.T) {
	bad := []byte(obfuscateHeader + "\n!!!not-base64!!!")
	if _, err := Unwrap(bad); err == nil {
		t.Fatalf("want decode error, got nil")
	}
}

func TestWrap_Empty(t *testing.T) {
	w := Wrap(nil)
	got, err := Unwrap(w)
	if err != nil {
		t.Fatalf("Unwrap: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty, got %q", got)
	}
}

func min(a, b int) int { if a < b { return a }; return b }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config -run 'TestWrap|TestUnwrap'`
Expected: FAIL with "undefined: Wrap" / "undefined: Unwrap" / "undefined: obfuscateHeader"

- [ ] **Step 3: Implement obfuscate.go**

```go
// internal/config/obfuscate.go
package config

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// obfuscateHeader is the literal first line of every obfuscated credential
// file. Plain text by design so users can grep their config dir and learn
// what the file is. The "not encrypted" wording is intentional — this layer
// defends against accidental disclosure to LLM tools that read files
// verbatim, not against a determined attacker.
const obfuscateHeader = "# shell3-obfuscated-v1 — not encrypted; do not paste contents"

// obfuscateKey is a 32-byte repeating key derived from a fixed string. The
// key is compiled into the binary; anyone with the source can decode the
// file. That is acceptable: the goal is to make credentials.shell3 look
// like opaque bytes when an automated reader (Claude, Cursor, grep) walks
// the home directory, so secrets do not leak by accident into transcripts
// or context windows.
var obfuscateKey = func() []byte {
	sum := sha256.Sum256([]byte("shell3-creds-obfuscation-v1"))
	return sum[:]
}()

// Wrap obfuscates plaintext into the on-disk format: magic header line +
// base64-encoded XOR-encrypted body. Always succeeds.
func Wrap(plaintext []byte) []byte {
	xored := make([]byte, len(plaintext))
	for i, b := range plaintext {
		xored[i] = b ^ obfuscateKey[i%len(obfuscateKey)]
	}
	encoded := base64.StdEncoding.EncodeToString(xored)
	return []byte(obfuscateHeader + "\n" + encoded)
}

// Unwrap reverses Wrap. Returns an error when the header is missing or the
// body is not valid base64.
func Unwrap(blob []byte) ([]byte, error) {
	s := string(blob)
	if !strings.HasPrefix(s, obfuscateHeader) {
		return nil, errors.New("config: obfuscated file: missing magic header")
	}
	body := strings.TrimPrefix(s, obfuscateHeader)
	body = strings.TrimLeft(body, "\r\n")
	xored, err := base64.StdEncoding.DecodeString(strings.TrimSpace(body))
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(xored))
	for i, b := range xored {
		out[i] = b ^ obfuscateKey[i%len(obfuscateKey)]
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config -run 'TestWrap|TestUnwrap'`
Expected: PASS for all four tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/obfuscate.go internal/config/obfuscate_test.go
git commit -m "feat(config): add XOR-based credential obfuscation helper"
```

---

## Task 2: Implement CredStore

**Files:**
- Create: `internal/config/credstore.go`
- Test: `internal/config/credstore_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/credstore_test.go
package config

import (
	"path/filepath"
	"testing"
)

func TestCredStore_SetGetList(t *testing.T) {
	home := t.TempDir()
	store, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("LoadCredStore on empty home: %v", err)
	}
	if got := store.List(); len(got) != 0 {
		t.Fatalf("want empty list, got %v", got)
	}

	if err := store.Set("openai-prod", "openai", map[string]string{
		"base_url":      "https://api.openai.com/v1",
		"api_key":       "sk-test",
		"default_model": "gpt-4o",
	}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Reload from disk to confirm persistence.
	store2, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	adapter, fields, ok := store2.Get("openai-prod")
	if !ok {
		t.Fatal("Get: not found")
	}
	if adapter != "openai" || fields["api_key"] != "sk-test" {
		t.Fatalf("got adapter=%q api_key=%q", adapter, fields["api_key"])
	}
	if list := store2.List(); len(list) != 1 || list[0].Instance != "openai-prod" {
		t.Fatalf("List: %+v", list)
	}
}

func TestCredStore_Update(t *testing.T) {
	home := t.TempDir()
	store, _ := LoadCredStore(home)
	store.Set("codex", "codex", map[string]string{
		"access_token":  "old",
		"refresh_token": "rt",
	})
	if err := store.Update("codex", func(f map[string]string) error {
		f["access_token"] = "new"
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	store2, _ := LoadCredStore(home)
	_, fields, _ := store2.Get("codex")
	if fields["access_token"] != "new" || fields["refresh_token"] != "rt" {
		t.Fatalf("Update did not persist correctly: %+v", fields)
	}
}

func TestCredStore_Delete(t *testing.T) {
	home := t.TempDir()
	store, _ := LoadCredStore(home)
	store.Set("a", "openai", map[string]string{"api_key": "x"})
	store.Set("b", "openai", map[string]string{"api_key": "y"})
	if err := store.Delete("a"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, ok := store.Get("a"); ok {
		t.Fatal("Delete left record")
	}
	if _, _, ok := store.Get("b"); !ok {
		t.Fatal("Delete clobbered other record")
	}
}

func TestCredStore_FilePathAndPerm(t *testing.T) {
	home := t.TempDir()
	store, _ := LoadCredStore(home)
	store.Set("openai", "openai", map[string]string{"api_key": "k"})
	path := filepath.Join(home, ".shell3", "credentials.shell3")
	info, err := osStat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("perm = %o, want 0600", mode)
	}
}
```

Add helper at the bottom of the test file:

```go
import "os"

func osStat(p string) (os.FileInfo, error) { return os.Stat(p) }
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config -run TestCredStore`
Expected: FAIL with "undefined: LoadCredStore" / "undefined: CredStore".

- [ ] **Step 3: Implement credstore.go**

```go
// internal/config/credstore.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gopkg.in/yaml.v3"
)

// instanceRecord is the on-disk shape of a single credential instance.
type instanceRecord struct {
	Adapter string            `yaml:"adapter"`
	Fields  map[string]string `yaml:"fields"`
}

// credsFile is the on-disk root object inside the obfuscated body.
type credsFile struct {
	Version   int                       `yaml:"version"`
	Instances map[string]instanceRecord `yaml:"instances"`
}

// InstanceMeta is the public summary of one configured instance.
type InstanceMeta struct {
	Instance string
	Adapter  string
}

// CredStore is the unified credential store backed by
// ~/.shell3/credentials.shell3. Instances are keyed by user-chosen name
// (e.g. "ollama-local", "codex"); each record carries its adapter name and
// a flat string-keyed bag of fields. The on-disk file is XOR-obfuscated
// (see obfuscate.go) and never written in plaintext.
//
// Methods are concurrency-safe within one process via mu. Cross-process
// safety relies on atomic write + last-writer-wins; the codex token
// refresh path uses Update under mu.
type CredStore struct {
	homeDir string

	mu   sync.Mutex
	data credsFile
}

// LoadCredStore reads ~/.shell3/credentials.shell3 if present, otherwise
// returns an empty store ready for Set/Save. Missing parent dirs are not
// created here — Save handles that.
func LoadCredStore(homeDir string) (*CredStore, error) {
	c := &CredStore{
		homeDir: homeDir,
		data:    credsFile{Version: 1, Instances: map[string]instanceRecord{}},
	}
	path := credsPath(homeDir)
	blob, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return c, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	plaintext, err := Unwrap(blob)
	if err != nil {
		return nil, fmt.Errorf("config: unwrap %s: %w", path, err)
	}
	if err := yaml.Unmarshal(plaintext, &c.data); err != nil {
		return nil, fmt.Errorf("config: parse credentials: %w", err)
	}
	if c.data.Instances == nil {
		c.data.Instances = map[string]instanceRecord{}
	}
	return c, nil
}

// credsPath returns the canonical on-disk path. Exposed for migrate.go.
func credsPath(homeDir string) string {
	return filepath.Join(homeDir, ".shell3", "credentials.shell3")
}

// Set writes (or overwrites) one instance and persists immediately. fields
// is copied so callers can keep mutating their map.
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

// Get returns the adapter name and a copy of the field bag. ok is false
// when the instance is unknown.
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

// Update applies fn to a snapshot of the instance's fields and persists
// the result. Used by adapters whose tokens refresh mid-session.
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

// Delete removes an instance. No-op when the instance is unknown.
func (c *CredStore) Delete(instance string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.data.Instances[instance]; !ok {
		return nil
	}
	delete(c.data.Instances, instance)
	return c.saveLocked()
}

// List returns the configured instances sorted by name.
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

// HomeDir returns the home directory the store was loaded against.
func (c *CredStore) HomeDir() string { return c.homeDir }

// saveLocked marshals data, wraps with the obfuscation layer, and writes
// atomically (tmp + rename) with 0600 perms. Caller must hold c.mu.
func (c *CredStore) saveLocked() error {
	dir := filepath.Join(c.homeDir, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	plaintext, err := yaml.Marshal(c.data)
	if err != nil {
		return fmt.Errorf("config: marshal credentials: %w", err)
	}
	wrapped := Wrap(plaintext)
	path := credsPath(c.homeDir)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, wrapped, 0600); err != nil {
		return fmt.Errorf("config: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("config: rename tmp: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config -run TestCredStore`
Expected: PASS for all four `TestCredStore_*` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/credstore.go internal/config/credstore_test.go
git commit -m "feat(config): add unified obfuscated CredStore"
```

---

## Task 3: Migration from legacy files

**Files:**
- Create: `internal/config/migrate.go`
- Test: `internal/config/migrate_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/migrate_test.go
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrate_FromLegacyYAML(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := `providers:
  openai-prod:
    api_key: sk-test
    base_url: https://api.openai.com/v1
    default_model: gpt-4o
  ollama-local:
    api_key: ""
    base_url: http://localhost:11434/v1
    default_model: llama3.2
`
	if err := os.WriteFile(filepath.Join(dir, "credentials.yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("LoadCredStore: %v", err)
	}
	adapter, fields, ok := store.Get("openai-prod")
	if !ok || adapter != "openai" || fields["api_key"] != "sk-test" {
		t.Fatalf("openai-prod not migrated: ok=%v adapter=%q fields=%v", ok, adapter, fields)
	}
	_, _, ok = store.Get("ollama-local")
	if !ok {
		t.Fatal("ollama-local not migrated")
	}

	// Old file backed up, new file present.
	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml")); !os.IsNotExist(err) {
		t.Fatalf("legacy file should have been renamed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.shell3")); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
}

func TestMigrate_FromLegacyCodexTokens(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano)
	tokens := map[string]any{
		"access_token":  "at",
		"refresh_token": "rt",
		"id_token":      "idt",
		"account_id":    "acc",
		"plan_type":     "pro",
		"expires_at":    expires,
	}
	data, _ := json.Marshal(tokens)
	if err := os.WriteFile(filepath.Join(dir, "codex_tokens.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("LoadCredStore: %v", err)
	}
	adapter, fields, ok := store.Get("codex")
	if !ok {
		t.Fatal("codex instance missing")
	}
	if adapter != "codex" {
		t.Fatalf("adapter=%q want codex", adapter)
	}
	for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id", "plan_type", "expires_at"} {
		if fields[k] == "" {
			t.Errorf("missing field %s", k)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "codex_tokens.json")); !os.IsNotExist(err) {
		t.Errorf("codex_tokens.json should be removed after migrate")
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	home := t.TempDir()
	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate on empty home: %v", err)
	}
	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate second pass: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config -run TestMigrate`
Expected: FAIL with "undefined: Migrate".

- [ ] **Step 3: Implement migrate.go**

```go
// internal/config/migrate.go
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Migrate imports legacy ~/.shell3/credentials.yaml (multi-provider OpenAI-
// compatible records) and ~/.shell3/codex_tokens.json (codex OAuth tokens)
// into the unified CredStore at ~/.shell3/credentials.shell3.
//
// Safe to run on every startup: missing inputs are skipped and a successful
// run renames credentials.yaml → credentials.yaml.bak and removes
// codex_tokens.json. A second invocation is therefore a no-op.
func Migrate(homeDir string) error {
	store, err := LoadCredStore(homeDir)
	if err != nil {
		return err
	}

	if err := migrateLegacyYAML(homeDir, store); err != nil {
		return err
	}
	if err := migrateCodexTokens(homeDir, store); err != nil {
		return err
	}
	return nil
}

func migrateLegacyYAML(homeDir string, store *CredStore) error {
	path := filepath.Join(homeDir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: read legacy credentials: %w", err)
	}
	var legacy struct {
		Providers map[string]struct {
			APIKey       string `yaml:"api_key"`
			BaseURL      string `yaml:"base_url"`
			DefaultModel string `yaml:"default_model,omitempty"`
		} `yaml:"providers"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil {
		return fmt.Errorf("config: parse legacy credentials: %w", err)
	}
	for instance, p := range legacy.Providers {
		fields := map[string]string{
			"api_key":       p.APIKey,
			"base_url":      p.BaseURL,
			"default_model": p.DefaultModel,
		}
		if err := store.Set(instance, "openai", fields); err != nil {
			return err
		}
	}
	if err := os.Rename(path, path+".bak"); err != nil {
		return fmt.Errorf("config: backup legacy credentials: %w", err)
	}
	return nil
}

func migrateCodexTokens(homeDir string, store *CredStore) error {
	path := filepath.Join(homeDir, ".shell3", "codex_tokens.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("config: read legacy codex tokens: %w", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("config: parse legacy codex tokens: %w", err)
	}
	fields := map[string]string{}
	for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id", "plan_type", "expires_at"} {
		if v, ok := raw[k]; ok {
			fields[k] = fmt.Sprint(v)
		}
	}
	if err := store.Set("codex", "codex", fields); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("config: remove legacy codex tokens: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config -run TestMigrate`
Expected: PASS for all three tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/migrate.go internal/config/migrate_test.go
git commit -m "feat(config): migrate legacy creds + codex tokens into CredStore"
```

---

## Task 4: New `Provider` interface in `internal/llm`

**Files:**
- Modify: `internal/llm/provider.go`

- [ ] **Step 1: Replace provider.go with the new interface**

```go
// internal/llm/provider.go
package llm

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/weatherjean/shell3/internal/config"
)

// Streamer is the streaming surface every LLM client exposes.
type Streamer interface {
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error
}

// ModelSetter is implemented by Streamers that can swap their target model
// in place. cmd/shell3/run.go's modelSwitcher prefers this over rebuilding
// the client when the provider is unchanged.
type ModelSetter interface {
	SetModel(model string)
}

// TrafficInspector is implemented by Streamers that buffer the last raw
// HTTP request/response they handled. internal/chat/turn.go uses this to
// dump upstream context on stream errors.
type TrafficInspector interface {
	LastTraffic() (req, res []byte)
}

// ReasoningInspector is implemented by Streamers that side-channel
// "reasoning" text out of band of the standard delta stream (e.g. the
// OpenRouter "reasoning" field).
type ReasoningInspector interface {
	LastReasoning() string
}

// Provider is a self-registering LLM backend. Each adapter package
// (internal/adapters/<name>) owns one Provider impl, registers it via
// Register from init(), and is wired into cmd/shell3/main.go via blank
// import.
type Provider interface {
	// Name returns the adapter name (matches the registry key).
	Name() string

	// SingleInstance reports whether the adapter supports exactly one
	// configured instance. When true, the instance name is forced to
	// equal Name() and re-running auth overwrites it. When false, users
	// may configure multiple named instances (e.g. several OpenAI-
	// compatible endpoints).
	SingleInstance() bool

	// Auth runs the adapter's interactive credential setup, persisting
	// results via store. instance is the chosen instance name (forced to
	// Name() when SingleInstance() is true).
	Auth(ctx context.Context, w io.Writer, store *config.CredStore, instance string) error

	// NewClient constructs a ready-to-use Streamer for the given
	// instance + model. Adapters read their fields from store via
	// store.Get(instance) / store.Update(instance, ...).
	NewClient(ctx context.Context, store *config.CredStore, instance, model string) (Streamer, error)

	// Models lists the model identifiers this adapter exposes for the
	// given instance. May read store for instance-defined defaults.
	Models(store *config.CredStore, instance string) []string
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

// Register adds a Provider under name. Panics on duplicate registration.
func Register(name string, p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, dup := registry[name]; dup {
		panic(fmt.Sprintf("llm: provider %q already registered", name))
	}
	registry[name] = p
}

// Get returns the Provider registered under name.
func Get(name string) (Provider, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// Registered returns the names of all registered providers, sorted.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	out := make([]string, 0, len(registry))
	for name := range registry {
		out = append(out, name)
	}
	return out
}
```

- [ ] **Step 2: Run a build to confirm the package itself compiles**

Run: `go build ./internal/llm/...`
Expected: success. Existing `internal/providers/codex` will be broken until Task 6 — that is expected and gets fixed there.

- [ ] **Step 3: Commit**

```bash
git add internal/llm/provider.go
git commit -m "feat(llm): expand Provider interface for adapter unification"
```

---

## Task 5: Lift `bodyTap` + Client into `internal/adapters/openai`

**Files:**
- Create: `internal/adapters/openai/client.go` (move of `internal/llm/client.go`)
- Create: `internal/adapters/openai/client_test.go` (move of `internal/llm/client_test.go`)
- Modify: delete `internal/llm/client.go` and `internal/llm/client_test.go`

- [ ] **Step 1: Move the file**

```bash
git mv internal/llm/client.go internal/adapters/openai/client.go
git mv internal/llm/client_test.go internal/adapters/openai/client_test.go
```

- [ ] **Step 2: Rewrite the package declaration and imports**

In `internal/adapters/openai/client.go` change the first line from `package llm` to:

```go
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	openaiapi "github.com/sashabaranov/go-openai"

	"github.com/weatherjean/shell3/internal/llm"
)
```

Then everywhere in the file replace bare references to `Message`, `ToolDefinition`, `ToolCall`, `Usage`, `StreamEvent` with `llm.Message`, `llm.ToolDefinition`, `llm.ToolCall`, `llm.Usage`, `llm.StreamEvent`. Replace bare references to the `openai` Go alias (the function calls like `openai.ChatCompletionRequest`) with `openaiapi.ChatCompletionRequest` etc., since the package itself is now named `openai` and the SDK has been re-aliased.

Rename `Client` → `Client` (no rename, but it now lives at `openai.Client`). The exported helpers `toOpenAI` and `toOpenAITools` stay package-internal (lowercase).

- [ ] **Step 3: Update the moved test file**

In `internal/adapters/openai/client_test.go` change `package llm` → `package openai`. Adjust any references that previously used `llm.X` to `X` (when referring to types defined in the test's own package) and import `internal/llm` for the message types. If the test references `*Client` directly, that still works.

- [ ] **Step 4: Run the moved tests**

Run: `go test ./internal/adapters/openai`
Expected: PASS — same coverage as before, now under the new package.

- [ ] **Step 5: Confirm `internal/llm` no longer references go-openai**

Run: `go build ./internal/llm/...`
Expected: success — the package is now interfaces + value types only.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "refactor(openai): move OpenAI-compatible client to adapters/openai"
```

---

## Task 6: Move codex into `internal/adapters/codex`

**Files:**
- All files under `internal/providers/codex/` move to `internal/adapters/codex/`
- Modify: every internal-import path mentioning `internal/providers/codex` updates to `internal/adapters/codex`

- [ ] **Step 1: Move the directory**

```bash
git mv internal/providers/codex internal/adapters/codex
rmdir internal/providers 2>/dev/null || true
```

- [ ] **Step 2: Update internal imports**

```bash
grep -rl 'internal/providers/codex' internal cmd | xargs sed -i.bak 's|internal/providers/codex|internal/adapters/codex|g'
find internal cmd -name '*.bak' -delete
```

- [ ] **Step 3: Run a build**

Run: `go build ./...`
Expected: failure — `register.go`'s `Provider` impl no longer matches the new interface signature. That gets fixed in Task 7. Confirm the failure is in `internal/adapters/codex/register.go` and `cmd/shell3/run.go` (because the OpenAI Streamer construction was removed in Task 5 from `internal/llm`). Exit non-zero is expected; do not fix here.

- [ ] **Step 4: Commit the move only**

```bash
git add -A
git commit -m "refactor(codex): move provider into adapters/codex"
```

---

## Task 7: Adapt codex `register.go` + token storage to new `Provider` + `CredStore`

**Files:**
- Modify: `internal/adapters/codex/register.go`
- Modify: `internal/adapters/codex/tokens.go`
- Modify: `internal/adapters/codex/client.go` (small: `newClient` signature + token mutation path)
- Modify: `internal/adapters/codex/oauth.go` (where `runBrowserFlow` writes tokens)

- [ ] **Step 1: Rewrite tokens.go to use CredStore exclusively**

```go
// internal/adapters/codex/tokens.go
package codex

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/config"
)

// instanceName is the stable instance key used in the unified CredStore.
// Codex is a single-instance adapter — auth always overwrites this entry.
const instanceName = "codex"

// Tokens is the in-memory shape used by the rest of this package.
type Tokens struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	PlanType     string
	ExpiresAt    time.Time
}

// IsExpired reports whether the access token is within the skew window of
// expiry. A 60-second skew matches the official Codex CLI.
func (t *Tokens) IsExpired() bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return time.Now().Add(60 * time.Second).After(t.ExpiresAt)
}

// LoadTokens reads the codex instance from store. Returns a friendly
// not-authenticated error when the instance is missing.
func LoadTokens(store *config.CredStore) (*Tokens, error) {
	_, fields, ok := store.Get(instanceName)
	if !ok {
		return nil, fmt.Errorf("codex: not authenticated — run: shell3 auth --provider=codex")
	}
	expiresAt, _ := time.Parse(time.RFC3339Nano, fields["expires_at"])
	return &Tokens{
		AccessToken:  fields["access_token"],
		RefreshToken: fields["refresh_token"],
		IDToken:      fields["id_token"],
		AccountID:    fields["account_id"],
		PlanType:     fields["plan_type"],
		ExpiresAt:    expiresAt,
	}, nil
}

// SaveTokens writes the codex instance, overwriting whatever was there.
func SaveTokens(store *config.CredStore, t *Tokens) error {
	fields := map[string]string{
		"access_token":  t.AccessToken,
		"refresh_token": t.RefreshToken,
		"id_token":      t.IDToken,
		"account_id":    t.AccountID,
		"plan_type":     t.PlanType,
		"expires_at":    t.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	return store.Set(instanceName, "codex", fields)
}

// decodedIDTokenClaims returns the JWT payload as a JSON string for diagnostics.
func decodedIDTokenClaims(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return "<unparseable id_token>"
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Sprintf("<base64 decode failed: %v>", err)
	}
	return string(payload)
}

// idTokenClaims, extractIDTokenClaims, exchangeCode, refreshTokens,
// postToken, tokenResponse, authHTTPClient — KEEP existing implementations
// from the old tokens.go.  Only LoadTokens / SaveTokens / tokensPath /
// the file-IO portion change.
```

Then in the same file, **paste back unchanged** all of: `idTokenClaims`, `extractIDTokenClaims`, `tokenResponse`, `exchangeCode`, `refreshTokens`, `postToken`, `authHTTPClient` from the previous version. Remove `tokensPath` entirely.

- [ ] **Step 2: Update client.go to take a CredStore**

In `internal/adapters/codex/client.go`:

Replace the `newClient` signature:

```go
func newClient(store *config.CredStore, model string) (*client, error) {
	t, err := LoadTokens(store)
	if err != nil {
		return nil, err
	}
	return &client{
		model:     model,
		store:     store,
		sessionID: uuid.NewString(),
		tokens:    t,
	}, nil
}
```

Replace the `client` struct fields:

```go
type client struct {
	model     string
	store     *config.CredStore
	sessionID string

	mu     sync.Mutex
	tokens *Tokens
}
```

Replace `c.homeDir` references in `ensureFreshToken` and `forceRefresh` with `SaveTokens(c.store, next)`.

Add the `config` import to `client.go`:

```go
"github.com/weatherjean/shell3/internal/config"
```

- [ ] **Step 3: Update oauth.go where runBrowserFlow persists tokens**

Adjust `runBrowserFlow`'s signature so it takes a `*config.CredStore` instead of `homeDir string`. Inside, replace `SaveTokens(homeDir, t)` with `SaveTokens(store, t)`.

The function's call sites are exactly two: `register.go` (now updated below) and any tests inside `internal/adapters/codex/`. Update tests in step 6.

- [ ] **Step 4: Rewrite register.go to satisfy the new Provider interface**

```go
// internal/adapters/codex/register.go
package codex

import (
	"context"
	"io"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

type provider struct{}

func init() { llm.Register("codex", &provider{}) }

func (*provider) Name() string         { return "codex" }
func (*provider) SingleInstance() bool { return true }

// Auth runs the PKCE OAuth flow and persists tokens via store.
func (*provider) Auth(ctx context.Context, w io.Writer, store *config.CredStore, instance string) error {
	_ = instance // codex is single-instance; key is fixed.
	_, err := runBrowserFlow(ctx, store, w)
	return err
}

// NewClient builds a Streamer backed by the Codex Responses API.
func (*provider) NewClient(ctx context.Context, store *config.CredStore, instance, model string) (llm.Streamer, error) {
	_ = instance
	_ = ctx
	return newClient(store, model)
}

// Models lists the Codex models exposed via the ChatGPT subscription tier.
func (*provider) Models(_ *config.CredStore, _ string) []string {
	return []string{
		"gpt-5.1-codex",
		"gpt-5.1-codex-mini",
		"gpt-5.2",
		"gpt-5.3-codex",
		"gpt-5.4",
	}
}
```

- [ ] **Step 5: Drop the now-unused `homeDir` helper**

Search `internal/adapters/codex/` for `homeDir()` (a small helper that called `os.UserHomeDir()`). It is now unused. Delete the function and its import of `os` if no other code uses it.

```bash
grep -rn 'homeDir' internal/adapters/codex
```

If the only hits are the function definition and a now-removed call site, delete the definition.

- [ ] **Step 6: Update any test in `internal/adapters/codex/` that calls `runBrowserFlow` or `LoadTokens` / `SaveTokens` with the new signatures**

If `internal/adapters/codex/` has tests that wrote tokens via a `homeDir` `t.TempDir()`, they now need to construct a `*config.CredStore` via `config.LoadCredStore(t.TempDir())` instead.

Replace patterns like:

```go
home := t.TempDir()
codex.SaveTokens(home, &codex.Tokens{...})
```

with:

```go
home := t.TempDir()
store, err := config.LoadCredStore(home)
if err != nil { t.Fatal(err) }
if err := codex.SaveTokens(store, &codex.Tokens{...}); err != nil { t.Fatal(err) }
```

(Add the `config` import.)

- [ ] **Step 7: Build + test**

Run: `go test ./internal/adapters/codex`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(codex): adopt new Provider interface + CredStore"
```

---

## Task 8: OpenAI adapter `register.go` + `auth.go`

**Files:**
- Create: `internal/adapters/openai/register.go`
- Create: `internal/adapters/openai/auth.go`
- Create: `internal/adapters/openai/auth_test.go`

- [ ] **Step 1: Write the failing test for Auth**

```go
// internal/adapters/openai/auth_test.go
package openai

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestProviderAuth_PromptsAndPersists(t *testing.T) {
	home := t.TempDir()
	store, err := config.LoadCredStore(home)
	if err != nil {
		t.Fatal(err)
	}
	in := strings.NewReader(strings.Join([]string{
		"http://localhost:11434/v1",
		"",
		"llama3.2",
		"",
	}, "\n"))
	var out bytes.Buffer
	p := &provider{stdin: in}
	if err := p.Auth(context.Background(), &out, store, "ollama-local"); err != nil {
		t.Fatalf("Auth: %v", err)
	}
	adapter, fields, ok := store.Get("ollama-local")
	if !ok {
		t.Fatal("instance not persisted")
	}
	if adapter != "openai" {
		t.Fatalf("adapter=%q want openai", adapter)
	}
	if fields["base_url"] != "http://localhost:11434/v1" || fields["default_model"] != "llama3.2" {
		t.Fatalf("fields=%v", fields)
	}
	if !strings.Contains(out.String(), "Configure an OpenAI-compatible") {
		t.Fatalf("missing header in output:\n%s", out.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/adapters/openai -run TestProviderAuth`
Expected: FAIL with "undefined: provider".

- [ ] **Step 3: Implement register.go**

```go
// internal/adapters/openai/register.go
package openai

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

// provider is the llm.Provider impl for the OpenAI-compatible adapter.
// stdin is parameterized for tests; nil means os.Stdin (set by init()).
type provider struct {
	stdin io.Reader
}

func init() { llm.Register("openai", &provider{}) }

func (*provider) Name() string         { return "openai" }
func (*provider) SingleInstance() bool { return false }

// NewClient reads the instance's fields from store and builds a Client.
// model overrides the instance's default_model when non-empty.
func (*provider) NewClient(_ context.Context, store *config.CredStore, instance, model string) (llm.Streamer, error) {
	adapter, fields, ok := store.Get(instance)
	if !ok {
		return nil, fmt.Errorf("openai: no instance %q — run: shell3 auth", instance)
	}
	if adapter != "openai" {
		return nil, fmt.Errorf("openai: instance %q has adapter %q", instance, adapter)
	}
	if model == "" {
		model = firstModel(fields["default_model"])
	}
	return NewClient(fields["base_url"], fields["api_key"], model), nil
}

// Models returns the comma-separated default_model list.
func (*provider) Models(store *config.CredStore, instance string) []string {
	_, fields, ok := store.Get(instance)
	if !ok {
		return nil
	}
	out := []string{}
	for _, m := range strings.Split(fields["default_model"], ",") {
		if m := strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func firstModel(csv string) string {
	for _, m := range strings.Split(csv, ",") {
		if m := strings.TrimSpace(m); m != "" {
			return m
		}
	}
	return ""
}
```

- [ ] **Step 4: Implement auth.go**

```go
// internal/adapters/openai/auth.go
package openai

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
)

// Auth prompts for an OpenAI-compatible instance's connection settings and
// persists them. instance is the chosen instance name (e.g. "ollama-local").
func (p *provider) Auth(_ context.Context, w io.Writer, store *config.CredStore, instance string) error {
	if instance == "" {
		return fmt.Errorf("openai: instance name required")
	}
	in := p.stdin
	if in == nil {
		in = os.Stdin
	}
	scanner := bufio.NewScanner(in)

	fmt.Fprintln(w, "Configure an OpenAI-compatible LLM provider.")
	fmt.Fprintln(w, "Works with Ollama, OpenAI, Anthropic (via proxy), Together, OpenRouter, etc.")
	fmt.Fprintln(w)

	baseURL := promptLine(scanner, w, "Base URL (e.g. http://localhost:11434/v1 or https://api.openai.com/v1): ")
	apiKey := promptLine(scanner, w, "API key (leave empty if not required): ")
	model := promptLine(scanner, w, "Default model (comma-separate for multiple, e.g. gpt-4o,gpt-4o-mini): ")

	if err := store.Set(instance, "openai", map[string]string{
		"base_url":      baseURL,
		"api_key":       apiKey,
		"default_model": model,
	}); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nInstance %q saved.\n", instance)
	return nil
}

func promptLine(s *bufio.Scanner, w io.Writer, q string) string {
	fmt.Fprint(w, q)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/adapters/openai -run TestProviderAuth`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapters/openai/auth.go internal/adapters/openai/auth_test.go internal/adapters/openai/register.go
git commit -m "feat(openai): adapter Provider + interactive auth"
```

---

## Task 9: Wire `cmd/shell3/main.go` to import both adapters

**Files:**
- Modify: `cmd/shell3/main.go`

- [ ] **Step 1: Update the blank imports**

Replace the existing block:

```go
	// Self-registering OAuth providers. Each package's init() calls
	// llm.Register; the main app dispatches generically via llm.Get.
	_ "github.com/weatherjean/shell3/internal/providers/codex" // codex-compat: ChatGPT subscription auth
```

with:

```go
	// Self-registering adapters. Each package's init() calls llm.Register;
	// the main app dispatches generically via llm.Get.
	_ "github.com/weatherjean/shell3/internal/adapters/codex"  // codex: ChatGPT subscription auth
	_ "github.com/weatherjean/shell3/internal/adapters/openai" // openai-compatible: Ollama, OpenAI, OpenRouter, etc.
```

- [ ] **Step 2: Run build + ensure tests still pass for what we have**

Run: `go build ./...`
Expected: failure in `cmd/shell3/run.go` (still uses `llm.NewClient` and `*llm.Client`). Expected — gets fixed in Task 10. Confirm only that package fails.

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/main.go
git commit -m "refactor(main): blank-import openai + codex adapters"
```

---

## Task 10: Collapse `cmd/shell3/run.go` to single registry path

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Rewrite `runChat` connection setup + buildClient + modelSwitcher**

The new logic:

1. Run `config.Migrate(homeDir)` once at startup (idempotent). If it fails, surface the error.
2. Load `*config.CredStore`.
3. Resolve `(adapterName, instance, model)` from persona + flags + store. The persona's `Provider` field is now an instance name (matches the legacy multi-provider semantics — every entry in `credentials.yaml` was implicitly an instance). For `codex` (a single-instance adapter), the persona's `Provider="codex"` resolves to instance `"codex"`.
4. Look up the adapter via `llm.Get(adapterName)`. If found, call `p.NewClient(ctx, store, instance, model)`.

Replace the existing `runChat` body from the credentials load through the `client = openaiClient` block (roughly lines 71–236) with this:

```go
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return err
	}
	_ = creds // kept for legacy callers below, but the unified path uses store.

	if err := config.Migrate(homeDir); err != nil {
		return fmt.Errorf("migrate credentials: %w", err)
	}
	store, err := config.LoadCredStore(homeDir)
	if err != nil {
		return err
	}

	adapterName, instance, model := resolveConnection(pCfg.Provider, pCfg.Model, store, f)
	if adapterName == "" {
		return fmt.Errorf("no adapter configured — run: shell3 auth")
	}
	prov, ok := llm.Get(adapterName)
	if !ok {
		return fmt.Errorf("unknown adapter %q (registered: %v)", adapterName, llm.Registered())
	}

	noBash := pCfg.NoBash || f.noBash
	noMemory := pCfg.NoMemory || f.noMemory

	var st *store.Store
	storeDBPath := filepath.Join(cwd, coalesce(pCfg.DB, ".shell3/shell3.db"))
	if !noMemory {
		if s, err := openStore(storeDBPath); err == nil {
			st = s
			defer st.Close()
		}
	}
	// (… preserve existing skills/secrets/tools loading logic verbatim …)

	provName := instance // statusline uses the instance name
	statusLine := fmt.Sprintf("%s │ %s", provName, model)

	// Aggregate all known (instance, model) pairs across configured instances.
	var models []chat.ModelChoice
	for _, meta := range store.List() {
		if p, ok := llm.Get(meta.Adapter); ok {
			for _, m := range p.Models(store, meta.Instance) {
				models = append(models, chat.ModelChoice{Provider: meta.Instance, Model: m})
			}
		}
	}
	// Single-instance adapters with no configured row still expose their
	// hardcoded model list under the adapter name.
	for _, name := range llm.Registered() {
		p, _ := llm.Get(name)
		if !p.SingleInstance() {
			continue
		}
		if _, _, ok := store.Get(name); ok {
			continue // already covered by the loop above
		}
		for _, m := range p.Models(store, name) {
			models = append(models, chat.ModelChoice{Provider: name, Model: m})
		}
	}
	if len(models) == 0 {
		models = []chat.ModelChoice{{Provider: provName, Model: model}}
	}

	buildClient := func(inst, m string) (chat.LLMClient, error) {
		// Re-resolve the adapter for the given instance.
		_, _, found := store.Get(inst)
		adapter := adapterName
		if found {
			adapter, _, _ = store.Get(inst)
			meta := findInstance(store, inst)
			if meta.Adapter != "" {
				adapter = meta.Adapter
			}
		} else if _, ok := llm.Get(inst); ok {
			adapter = inst // single-instance adapter
		}
		p, ok := llm.Get(adapter)
		if !ok {
			return nil, fmt.Errorf("unknown adapter for instance %q", inst)
		}
		return p.NewClient(ctx, store, inst, m)
	}

	client, err := prov.NewClient(ctx, store, instance, model)
	if err != nil {
		return err
	}

	modelSwitcher := func(newInstance, newModel string) (chat.LLMClient, error) {
		if newInstance == "" || newInstance == instance {
			if ms, ok := client.(llm.ModelSetter); ok {
				ms.SetModel(newModel)
				model = newModel
				return nil, nil
			}
		}
		next, err := buildClient(newInstance, newModel)
		if err != nil {
			return nil, err
		}
		client = next
		instance = newInstance
		model = newModel
		return next, nil
	}
```

Add a helper near the bottom of the file:

```go
// findInstance returns the InstanceMeta for the given instance, or zero.
func findInstance(s *config.CredStore, instance string) config.InstanceMeta {
	for _, m := range s.List() {
		if m.Instance == instance {
			return m
		}
	}
	return config.InstanceMeta{}
}
```

- [ ] **Step 2: Update `resolveConnection` to use the new store**

Replace `resolveConnection` with this signature + body:

```go
func resolveConnection(providerHint, modelHint string, store *config.CredStore, f *runFlags) (adapter, instance, model string) {
	// CLI override: --base-url + --api-key creates an ephemeral openai-compat instance.
	if f.baseURL != "" && f.apiKey != "" {
		ephemeral := "_cli"
		_ = store.Set(ephemeral, "openai", map[string]string{
			"base_url":      f.baseURL,
			"api_key":       f.apiKey,
			"default_model": coalesce(f.model, modelHint, "llama3.2"),
		})
		return "openai", ephemeral, coalesce(f.model, modelHint, "llama3.2")
	}

	if providerHint != "" {
		// Hint may be an instance name OR a single-instance adapter name.
		if _, _, ok := store.Get(providerHint); ok {
			meta := findInstanceLite(store, providerHint)
			adapter = meta.Adapter
			instance = providerHint
		} else if _, ok := llm.Get(providerHint); ok {
			adapter = providerHint
			instance = providerHint
		}
	}

	if adapter == "" {
		// Pick the first configured instance alphabetically.
		list := store.List()
		if len(list) > 0 {
			adapter = list[0].Adapter
			instance = list[0].Instance
		}
	}

	model = coalesce(f.model, modelHint)
	if model == "" {
		if p, ok := llm.Get(adapter); ok {
			ms := p.Models(store, instance)
			if len(ms) > 0 {
				model = ms[0]
			}
		}
	}
	if model == "" {
		model = "llama3.2"
	}
	return
}

// findInstanceLite is a duplicate of findInstance kept here so resolveConnection
// stays self-contained.
func findInstanceLite(s *config.CredStore, instance string) config.InstanceMeta {
	for _, m := range s.List() {
		if m.Instance == instance {
			return m
		}
	}
	return config.InstanceMeta{}
}
```

(Drop the duplicate `findInstanceLite` if `findInstance` from Step 1 is callable here; either is fine.)

- [ ] **Step 3: Drop the old `*llm.Client` type assertions and `openaiClient` shadow**

Remove the locals `var openaiClient *llm.Client`, the assertion `if oc, ok := s.(*llm.Client); ok`, and the bifurcating `if _, ok := llm.Get(provName); ok` block in the original code. The new logic in Steps 1–2 replaces all of it.

- [ ] **Step 4: Update imports**

Remove imports of `internal/llm/client.go`-related symbols. Add (or keep):

```go
"github.com/weatherjean/shell3/internal/config"
"github.com/weatherjean/shell3/internal/chat"
"github.com/weatherjean/shell3/internal/llm"
```

Drop the now-unused `creds.Get(...)` / `creds.Providers` references — `_ = creds` placeholder makes it explicit while LoadCredentials is removed in Task 12.

- [ ] **Step 5: Build + run the existing tests**

Run: `go build ./...` then `go test ./...`
Expected: build succeeds, no behavior regressions in chat/config/persona tests.

- [ ] **Step 6: Smoke-test the binary**

```bash
go run ./cmd/shell3 -h
```

Expected: prints the help text with no panic. The blank imports in Task 9 mean `init()` runs for both adapters — a duplicate registration would panic here.

- [ ] **Step 7: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "refactor(run): single dispatch path through llm registry"
```

---

## Task 11: Adapter-menu auth UX

**Files:**
- Modify: `cmd/shell3/auth.go`

- [ ] **Step 1: Write the failing behaviour**

Replace `cmd/shell3/auth.go` with:

```go
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

func newAuthCommand() *cobra.Command {
	var providerFlag string
	var instanceFlag string
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Configure adapter credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			homeDir, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			if err := config.Migrate(homeDir); err != nil {
				return err
			}
			store, err := config.LoadCredStore(homeDir)
			if err != nil {
				return err
			}

			adapter := providerFlag
			if adapter == "" {
				adapter = pickAdapter(os.Stdin, os.Stdout)
				if adapter == "" {
					return fmt.Errorf("no adapter chosen")
				}
			}
			p, ok := llm.Get(adapter)
			if !ok {
				return fmt.Errorf("unknown adapter %q (registered: %v)", adapter, llm.Registered())
			}

			instance := instanceFlag
			if p.SingleInstance() {
				instance = p.Name()
			} else if instance == "" {
				instance = promptInstance(os.Stdin, os.Stdout, p.Name())
			}

			return p.Auth(cmd.Context(), os.Stdout, store, instance)
		},
	}
	cmd.Flags().StringVar(&providerFlag, "provider", "", "Adapter name (e.g. openai, codex)")
	cmd.Flags().StringVar(&instanceFlag, "instance", "", "Instance name (multi-instance adapters)")
	return cmd
}

// pickAdapter prints a numbered menu and reads the user's choice. Returns
// the chosen adapter name or "" on EOF.
func pickAdapter(in *os.File, out *os.File) string {
	names := llm.Registered()
	if len(names) == 0 {
		return ""
	}
	fmt.Fprintln(out, "Available adapters:")
	for i, n := range names {
		p, _ := llm.Get(n)
		marker := ""
		if p.SingleInstance() {
			marker = " (single-instance)"
		}
		fmt.Fprintf(out, "  %d) %s%s\n", i+1, n, marker)
	}
	fmt.Fprint(out, "Pick an adapter [1]: ")
	s := bufio.NewScanner(in)
	if !s.Scan() {
		return ""
	}
	choice := strings.TrimSpace(s.Text())
	if choice == "" {
		return names[0]
	}
	for i, n := range names {
		if choice == fmt.Sprintf("%d", i+1) || choice == n {
			return n
		}
	}
	return ""
}

func promptInstance(in *os.File, out *os.File, defaultName string) string {
	fmt.Fprintf(out, "Instance name [%s]: ", defaultName)
	s := bufio.NewScanner(in)
	if !s.Scan() {
		return defaultName
	}
	v := strings.TrimSpace(s.Text())
	if v == "" {
		return defaultName
	}
	return v
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Smoke test the menu**

Run interactively (or pipe a choice):

```bash
echo -e "1\nmytest\nhttp://localhost:11434/v1\n\nllama3.2" | go run ./cmd/shell3 auth
```

Expected: ends with "Instance \"mytest\" saved." and creates `~/.shell3/credentials.shell3`. (Use a scratch HOME for safety: `HOME=$(mktemp -d) ...`)

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/auth.go
git commit -m "feat(auth): adapter menu + multi-instance prompts"
```

---

## Task 12: Remove legacy `LoadCredentials` + `RunAuthInteractive`

**Files:**
- Modify: `internal/config/credentials.go`
- Modify: `internal/config/auth.go`
- Modify: `cmd/shell3/run.go` (remove `creds := config.LoadCredentials(...)` / `_ = creds`)
- Modify: any test under `internal/config/` that referenced the removed APIs

- [ ] **Step 1: Delete dead code**

```bash
rm internal/config/auth.go internal/config/auth_test.go
```

Replace `internal/config/credentials.go` with:

```go
// Package config loads credentials for shell3 adapters from
// ~/.shell3/credentials.shell3 via CredStore.
package config
```

Delete `internal/config/credentials_test.go` (its assertions about `LoadCredentials` no longer apply; coverage moved to `credstore_test.go` and `migrate_test.go`).

- [ ] **Step 2: Update run.go**

Remove the `creds, err := config.LoadCredentials(homeDir)` line and the `_ = creds` placeholder added in Task 10. Recheck imports — drop any now-unused `config` symbol references. Ensure `config.Migrate` and `config.LoadCredStore` calls remain.

- [ ] **Step 3: Build + test**

Run: `go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A
git commit -m "chore(config): drop legacy LoadCredentials + interactive auth"
```

---

## Task 13: Lift `trafficSource` interface to `internal/llm`

**Files:**
- Modify: `internal/chat/turn.go`

- [ ] **Step 1: Replace the local interface with the shared one**

In `internal/chat/turn.go`, delete the local `trafficSource` interface block (lines 17–22 of the snapshot) and update `dumpStreamError` to use `llm.TrafficInspector`:

```go
// dumpStreamError writes the failing turn's messages and the last raw
// HTTP traffic to .shell3/last_error.json under cfg.WorkDir.
func dumpStreamError(cfg Config, msgs []llm.Message, streamErr error) {
	if cfg.WorkDir == "" {
		return
	}
	var reqBody, resBody []byte
	if ts, ok := cfg.LLM.(llm.TrafficInspector); ok {
		reqBody, resBody = ts.LastTraffic()
	}
	rec := map[string]any{
		"timestamp":     time.Now().Format(time.RFC3339),
		"error":         streamErr.Error(),
		"messages":      msgs,
		"request_body":  string(reqBody),
		"response_body": string(resBody),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(cfg.WorkDir, ".shell3", "last_error.json")
	_ = os.WriteFile(path, data, 0644)
}
```

- [ ] **Step 2: Build + test**

Run: `go build ./... && go test ./internal/chat`
Expected: PASS. The OpenAI adapter's `*Client` already exposes `LastTraffic`, so the type assertion still succeeds.

- [ ] **Step 3: Commit**

```bash
git add internal/chat/turn.go
git commit -m "refactor(chat): use llm.TrafficInspector instead of local interface"
```

---

## Task 14: Update `cmd/shell3/init.go` to surface adapters

**Files:**
- Modify: `cmd/shell3/init.go`

- [ ] **Step 1: Extend the init command output**

After the existing `scaffold.InitProject(...)` call returns, print the registered adapters list and a hint to run `shell3 auth`:

```go
import "github.com/weatherjean/shell3/internal/llm"
// ...
RunE: func(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("git init not yet supported — coming soon")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	if err := scaffold.InitProject(cwd, homeDir); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Available adapters:")
	for _, name := range llm.Registered() {
		p, _ := llm.Get(name)
		marker := ""
		if p.SingleInstance() {
			marker = " (single-instance)"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  - %s%s\n", name, marker)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "Next: run `shell3 auth` to configure credentials.")
	return nil
},
```

- [ ] **Step 2: Build + smoke test**

```bash
HOME=$(mktemp -d) go run ./cmd/shell3 init 2>&1 | tail -10
```

Expected: ends with the "Available adapters:" block listing `codex` and `openai`, then the "Next:" hint.

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/init.go
git commit -m "feat(init): list registered adapters in command output"
```

---

## Task 15: Documentation

**Files:**
- Modify: `cmd/shell3/shell3.md`
- Modify: `README.md`

- [ ] **Step 1: Rewrite the Credentials section in `cmd/shell3/shell3.md`**

Locate the section starting at `### shell3 auth` and replace it with the following:

```markdown
### shell3 auth
Configure adapter credentials. With no flags, presents an adapter menu and prompts for any required fields. Credentials are stored at `~/.shell3/credentials.shell3` (XOR-obfuscated YAML).

```
shell3 auth                                # interactive: pick adapter, configure instance
shell3 auth --provider=openai              # configure an OpenAI-compatible instance
shell3 auth --provider=openai --instance=ollama-local
shell3 auth --provider=codex               # OAuth browser flow (single-instance)
```

**Storage format.** The credential file is wrapped with a fixed XOR + base64 layer behind a magic header. **This is obfuscation, not encryption.** It defends against accidental disclosure to LLM tools that walk your home directory and read files verbatim. Anyone with shell access (or this source tree) can reverse it trivially. Store actual high-value secrets in your OS keyring or a dedicated secret manager.

If `~/.shell3/credentials.yaml` or `~/.shell3/codex_tokens.json` exists from an older shell3, the first run automatically migrates them into `credentials.shell3` and renames `credentials.yaml` to `credentials.yaml.bak`.

### Adapters

shell3 ships with two adapters; new adapters live under `internal/adapters/<name>` and self-register via `init()`:

| Adapter  | Instance count | Auth                                | Models                                  |
|----------|----------------|-------------------------------------|-----------------------------------------|
| `openai` | many           | base URL + API key + default model  | per-instance `default_model` (CSV)      |
| `codex`  | one            | OAuth (ChatGPT subscription)        | hardcoded list (`gpt-5.1-codex`, …)     |

Pass an instance name via the persona's `provider:` field or `--persona` selection. For single-instance adapters, instance name = adapter name.
```

- [ ] **Step 2: Update `README.md` Docs section**

Just after the existing `Full documentation is embedded in the binary:` block, add a one-line pointer:

```markdown
Credentials are stored obfuscated (not encrypted) at `~/.shell3/credentials.shell3` — see `shell3 docs` for details and the threat model.
```

- [ ] **Step 3: Smoke-test the embedded docs path**

Run: `go run ./cmd/shell3 docs | grep -A2 "Storage format"`
Expected: prints the obfuscation framing paragraph.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/shell3.md README.md
git commit -m "docs: describe adapter model + obfuscated credential storage"
```

---

## Task 16: Final integration build + manual sanity

**Files:**
- (none — verification only)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 2: Run `go vet`**

Run: `go vet ./...`
Expected: clean.

- [ ] **Step 3: Build the binary**

Run: `make build`
Expected: produces `./shell3`.

- [ ] **Step 4: End-to-end auth + run with a scratch home**

```bash
TMP_HOME=$(mktemp -d)
HOME=$TMP_HOME ./shell3 auth --provider=openai --instance=ollama-local <<EOF
http://localhost:11434/v1

llama3.2
EOF
ls -la "$TMP_HOME/.shell3/"
file "$TMP_HOME/.shell3/credentials.shell3"
head -1 "$TMP_HOME/.shell3/credentials.shell3"
```

Expected:
- `credentials.shell3` exists, mode `-rw-------`
- First line is the magic header `# shell3-obfuscated-v1 — not encrypted; do not paste contents`
- `file(1)` reports something like ASCII text (because of the header) but the body is base64
- No `credentials.yaml` written

- [ ] **Step 5: Confirm secrets are not visible**

```bash
grep -F 'sk-' "$TMP_HOME/.shell3/credentials.shell3" || echo "secret not visible"
```

Expected: prints `secret not visible` (because we used an empty API key, but if a real key were entered it would also be unreadable).

- [ ] **Step 6: Commit (if any cleanup needed)**

If the verification surfaced anything to fix, commit those fixes here. Otherwise, no commit needed.

---

## Summary checklist

By the end of this plan:

- [x] `internal/llm/` contains only interfaces + value types (`provider.go`, `types.go`)
- [x] `internal/adapters/openai/` owns the OpenAI-compatible client + auth + register
- [x] `internal/adapters/codex/` (renamed from `internal/providers/codex/`) uses `CredStore` for tokens
- [x] `internal/config/credstore.go` is the single read/write point for credentials
- [x] `~/.shell3/credentials.shell3` is XOR-obfuscated; legacy files auto-migrate
- [x] `cmd/shell3/run.go` has one dispatch path through `llm.Get`
- [x] `cmd/shell3/auth.go` presents an adapter menu when no `--provider` is given
- [x] `shell3 init` lists registered adapters
- [x] `internal/chat/turn.go` uses `llm.TrafficInspector`, no chat-local interface duplication
- [x] Docs honestly describe the obfuscation layer's purpose and limits
