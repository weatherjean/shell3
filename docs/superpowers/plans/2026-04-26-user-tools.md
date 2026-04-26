# User-Defined Tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users define custom tools (name, description, JSON-schema parameters, shell command, optional secrets, optional before/after hooks) in `.shell3/tools/*.yaml` files. Loaded at startup, validated, merged with built-in tools, and dispatched the same way as built-ins. Includes a disabled `brave_search` example and `.env`-based secret loading.

**Architecture:** New `internal/usertools` package handles loading, validation, secret loading from `.shell3/.env`, and execution. `persona.Load` accepts an optional list of user tools and appends them after built-ins. `chat/turn.go` dispatcher falls through to `usertools.Run` for any tool name not handled by built-ins. Secrets pass through subprocess env only; tool output is scanned for secret values and redacted before returning to the LLM. Per-tool `before` (block/rewrite args) and `after` (rewrite output) hooks layer on top of the existing global `OnToolCall`/`OnToolResult` hooks. Scaffold creates `tools/`, an example `brave_search.yaml` (disabled), `.env.example`, and adds `.env` to the project gitignore.

**Tech Stack:** Go, `gopkg.in/yaml.v3`, stdlib `os/exec`, existing `internal/llm` types, existing `internal/hooks` package.

---

## File Structure

**Create:**
- `internal/usertools/usertools.go` — types (`Spec`, `Tool`), `LoadAll`, `Validate`, builtin-name reservation list
- `internal/usertools/dotenv.go` — `LoadDotEnv(path) (map[string]string, error)` — minimal `.env` parser, no shell expansion, supports `#` comments and `KEY=value` (optionally double-quoted)
- `internal/usertools/exec.go` — `Run(ctx, tool, rawArgs, secrets, releaser) (string, error)`; injects env, runs command via `bash -c`, applies before/after hooks, applies redaction
- `internal/usertools/redact.go` — `Redact(s string, secretValues []string) string`
- `internal/usertools/usertools_test.go` — load/validate tests
- `internal/usertools/dotenv_test.go` — parser tests
- `internal/usertools/exec_test.go` — exec, env injection, redaction, before/after hook tests

**Modify:**
- `internal/persona/persona.go` — add `UserTools []ToolDef` parameter (or extend `Load` signature) so user tools merge into `Persona.Tools`
- `internal/chat/chat.go` — add `UserTools map[string]usertools.Tool` and `Secrets map[string]string` fields to `Config`
- `internal/chat/turn.go` — extend dispatch in `runTurn` so unknown tool names fall through to `usertools.Run`
- `internal/chat/tools.go` — add `dispatchUserTool` helper invoked from `runTurn`
- `internal/scaffold/scaffold.go` — add `tools/` dir, `.env.example`, `brave_search.yaml`; update `defaultGitignore` to include `.env`
- `internal/scaffold/scaffold_test.go` — assert new files/dirs exist
- `cmd/shell3/run.go` — load `.shell3/.env` + user tools, pass to `persona.Load` and `chat.Config`
- `cmd/shell3/shell3.md` — add "User-Defined Tools" docs section

---

## Conventions Used Below

- `bashTimeout` (existing constant in `internal/chat/tools.go`) is `30 * time.Second` — reused as default tool timeout.
- All tests use Go's `testing` package, `t.TempDir()` for fixtures, table-driven where natural.
- Builtin reserved names (cannot be shadowed by user tools): `bash`, `shell_interactive`, `shell3_docs`, `memory_store`, `memory_list`, `memory_search`, `memory_remove`, `history_latest`, `history_search`.
- Tool YAML file basename does **not** have to match `name`. `name` field inside the file is authoritative.
- All commits use Conventional Commits format.

---

## Task 1: Create usertools package skeleton + Spec type

**Files:**
- Create: `internal/usertools/usertools.go`
- Test: `internal/usertools/usertools_test.go`

- [ ] **Step 1: Write the failing test for Spec YAML parse**

```go
// internal/usertools/usertools_test.go
package usertools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAll_ParsesSpec(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: hello
description: Say hello
enabled: true
parameters:
  type: object
  properties:
    who:
      type: string
  required: [who]
command: 'echo "hi $WHO"'
`
	if err := os.WriteFile(filepath.Join(dir, "hello.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}

	tools, warnings, err := LoadAll([]string{dir}, nil)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	if tools[0].Spec.Name != "hello" {
		t.Errorf("name: got %q", tools[0].Spec.Name)
	}
	if tools[0].Spec.Command != `echo "hi $WHO"` {
		t.Errorf("command: got %q", tools[0].Spec.Command)
	}
	if !tools[0].Spec.Enabled {
		t.Error("expected enabled")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/usertools/...`
Expected: FAIL with "no Go files" or undefined `LoadAll`.

- [ ] **Step 3: Implement Spec, Tool, and stub LoadAll**

```go
// internal/usertools/usertools.go
// Package usertools loads and runs user-defined tool specs from
// .shell3/tools/*.yaml files.
package usertools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Spec is the on-disk YAML format for a user tool.
type Spec struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Enabled     bool           `yaml:"enabled"`
	Parameters  map[string]any `yaml:"parameters"`
	Command     string         `yaml:"command"`
	Secrets     []string       `yaml:"secrets,omitempty"`
	Timeout     time.Duration  `yaml:"timeout,omitempty"`
	Cwd         string         `yaml:"cwd,omitempty"`
	Before      string         `yaml:"before,omitempty"`
	After       string         `yaml:"after,omitempty"`
}

// Tool is a loaded, validated user tool with its source path attached.
type Tool struct {
	Spec
	Path string
}

// reservedNames are built-in tool names that user tools cannot shadow.
var reservedNames = map[string]struct{}{
	"bash":              {},
	"shell_interactive": {},
	"shell3_docs":       {},
	"memory_store":      {},
	"memory_list":       {},
	"memory_search":     {},
	"memory_remove":     {},
	"history_latest":    {},
	"history_search":    {},
}

// LoadAll walks each dir in order and returns enabled, validated tools.
// Later dirs override earlier ones on name collision (project beats global).
// availableSecrets is the set of keys present in .env+OS env; tools that
// declare missing secrets are disabled with a warning.
func LoadAll(dirs []string, availableSecrets map[string]struct{}) (tools []Tool, warnings []string, err error) {
	byName := map[string]Tool{}
	for _, dir := range dirs {
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return nil, warnings, fmt.Errorf("usertools: read dir %s: %w", dir, readErr)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
				continue
			}
			path := filepath.Join(dir, name)
			data, rdErr := os.ReadFile(path)
			if rdErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: read: %v", path, rdErr))
				continue
			}
			var s Spec
			if uErr := yaml.Unmarshal(data, &s); uErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: parse: %v", path, uErr))
				continue
			}
			if vErr := Validate(s, availableSecrets); vErr != nil {
				warnings = append(warnings, fmt.Sprintf("%s: %v", path, vErr))
				continue
			}
			if !s.Enabled {
				continue
			}
			byName[s.Name] = Tool{Spec: s, Path: path}
		}
	}
	for _, t := range byName {
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, warnings, nil
}

// Validate checks a Spec for problems. Returns nil when usable.
func Validate(s Spec, availableSecrets map[string]struct{}) error {
	if s.Name == "" {
		return fmt.Errorf("name: required")
	}
	if !validName(s.Name) {
		return fmt.Errorf("name %q: must match [a-z][a-z0-9_]*", s.Name)
	}
	if _, reserved := reservedNames[s.Name]; reserved {
		return fmt.Errorf("name %q: reserved by built-in tool", s.Name)
	}
	if s.Description == "" {
		return fmt.Errorf("description: required")
	}
	if s.Command == "" {
		return fmt.Errorf("command: required")
	}
	if s.Parameters == nil {
		return fmt.Errorf("parameters: required (use {type: object, properties: {}} for no args)")
	}
	if t, _ := s.Parameters["type"].(string); t != "object" {
		return fmt.Errorf("parameters.type: must be \"object\"")
	}
	for _, sec := range s.Secrets {
		if _, ok := availableSecrets[sec]; !ok {
			return fmt.Errorf("secret %q: not set in .shell3/.env or environment", sec)
		}
	}
	return nil
}

func validName(s string) bool {
	if s == "" || !(s[0] >= 'a' && s[0] <= 'z') {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/usertools/...`
Expected: PASS.

- [ ] **Step 5: Add validation tests (table-driven)**

```go
// append to internal/usertools/usertools_test.go

func TestValidate(t *testing.T) {
	avail := map[string]struct{}{"FOO_KEY": {}}
	objParams := map[string]any{"type": "object", "properties": map[string]any{}}
	cases := []struct {
		name    string
		s       Spec
		wantErr string
	}{
		{"missing name", Spec{Description: "d", Command: "c", Parameters: objParams}, "name: required"},
		{"bad name", Spec{Name: "Bad-Name", Description: "d", Command: "c", Parameters: objParams}, "must match"},
		{"reserved name", Spec{Name: "bash", Description: "d", Command: "c", Parameters: objParams}, "reserved"},
		{"missing desc", Spec{Name: "ok", Command: "c", Parameters: objParams}, "description: required"},
		{"missing cmd", Spec{Name: "ok", Description: "d", Parameters: objParams}, "command: required"},
		{"missing params", Spec{Name: "ok", Description: "d", Command: "c"}, "parameters: required"},
		{"params not object", Spec{Name: "ok", Description: "d", Command: "c", Parameters: map[string]any{"type": "string"}}, "must be \"object\""},
		{"secret missing", Spec{Name: "ok", Description: "d", Command: "c", Parameters: objParams, Secrets: []string{"NOPE"}}, "not set"},
		{"ok with secret", Spec{Name: "ok", Description: "d", Command: "c", Parameters: objParams, Secrets: []string{"FOO_KEY"}}, ""},
		{"ok no secret", Spec{Name: "ok", Description: "d", Command: "c", Parameters: objParams}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.s, avail)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}
```

Add `"strings"` to the test file imports if not present.

- [ ] **Step 6: Run validation tests**

Run: `go test ./internal/usertools/...`
Expected: PASS.

- [ ] **Step 7: Add disabled + override + warnings tests**

```go
// append to usertools_test.go

func TestLoadAll_SkipsDisabled(t *testing.T) {
	dir := t.TempDir()
	yaml := `name: off_tool
description: d
enabled: false
parameters: {type: object, properties: {}}
command: 'echo'
`
	os.WriteFile(filepath.Join(dir, "off.yaml"), []byte(yaml), 0644)

	tools, _, err := LoadAll([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 0 {
		t.Fatalf("expected 0 tools, got %d", len(tools))
	}
}

func TestLoadAll_ProjectOverridesGlobal(t *testing.T) {
	global := t.TempDir()
	project := t.TempDir()
	g := `name: hello
description: from global
enabled: true
parameters: {type: object, properties: {}}
command: 'echo global'
`
	p := `name: hello
description: from project
enabled: true
parameters: {type: object, properties: {}}
command: 'echo project'
`
	os.WriteFile(filepath.Join(global, "hello.yaml"), []byte(g), 0644)
	os.WriteFile(filepath.Join(project, "hello.yaml"), []byte(p), 0644)

	tools, _, err := LoadAll([]string{global, project}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Description != "from project" {
		t.Fatalf("project should win: %+v", tools)
	}
}

func TestLoadAll_InvalidYieldsWarning(t *testing.T) {
	dir := t.TempDir()
	bad := `name: BAD
description: d
enabled: true
parameters: {type: object, properties: {}}
command: 'echo'
`
	os.WriteFile(filepath.Join(dir, "bad.yaml"), []byte(bad), 0644)

	tools, warnings, err := LoadAll([]string{dir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 0 {
		t.Fatal("expected no tools")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "must match") {
		t.Fatalf("expected name warning, got %v", warnings)
	}
}
```

- [ ] **Step 8: Run all usertools tests**

Run: `go test ./internal/usertools/... -v`
Expected: PASS — all four tests pass.

- [ ] **Step 9: Commit**

```bash
git add internal/usertools/usertools.go internal/usertools/usertools_test.go
git commit -m "feat(usertools): add Spec type, LoadAll, and Validate"
```

---

## Task 2: Implement .env loader

**Files:**
- Create: `internal/usertools/dotenv.go`
- Test: `internal/usertools/dotenv_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/usertools/dotenv_test.go
package usertools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	body := `# comment line
FOO=bar
EMPTY=
QUOTED="hello world"
WITH_EQ=a=b=c

# trailing blank lines below

`
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	want := map[string]string{
		"FOO":     "bar",
		"EMPTY":   "",
		"QUOTED":  "hello world",
		"WITH_EQ": "a=b=c",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("extra keys: %v", got)
	}
}

func TestLoadDotEnv_Missing(t *testing.T) {
	got, err := LoadDotEnv("/no/such/path/.env")
	if err != nil {
		t.Fatalf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestLoadDotEnv_PermissionWarn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	os.WriteFile(path, []byte("FOO=bar\n"), 0644)
	_, err := LoadDotEnv(path)
	if err == nil {
		t.Fatal("expected permission warning error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/usertools/ -run DotEnv`
Expected: FAIL with undefined `LoadDotEnv`.

- [ ] **Step 3: Implement dotenv.go**

```go
// internal/usertools/dotenv.go
package usertools

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// LoadDotEnv reads a KEY=value file. Missing files return an empty map (not
// an error). Returns an error if the file is world- or group-readable on
// Unix — secrets must be 0600.
//
// Format: lines starting with '#' are comments. Blank lines are skipped.
// Values may be wrapped in double quotes; quotes are stripped. No shell
// expansion. Anything after the first '=' is the value (so values may
// themselves contain '=').
func LoadDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("dotenv: open: %w", err)
	}
	defer f.Close()

	if runtime.GOOS != "windows" {
		fi, sErr := f.Stat()
		if sErr == nil {
			mode := fi.Mode().Perm()
			if mode&0o077 != 0 {
				return nil, fmt.Errorf("dotenv: %s has permissions %#o; tighten to 0600 (chmod 600 %s)", path, mode, path)
			}
		}
	}

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("dotenv: %s:%d: missing '='", path, lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dotenv: scan: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run dotenv tests**

Run: `go test ./internal/usertools/ -run DotEnv -v`
Expected: PASS — three tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/usertools/dotenv.go internal/usertools/dotenv_test.go
git commit -m "feat(usertools): add .env loader with 0600 permission check"
```

---

## Task 3: Implement Run (exec) — env injection, timeout, args

**Files:**
- Create: `internal/usertools/exec.go`
- Test: `internal/usertools/exec_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/usertools/exec_test.go
package usertools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func mkTool(cmd string, secrets []string) Tool {
	return Tool{
		Spec: Spec{
			Name:        "t",
			Description: "d",
			Enabled:     true,
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
			Command:     cmd,
			Secrets:     secrets,
			Timeout:     5 * time.Second,
		},
	}
}

func TestRun_EchoArgs(t *testing.T) {
	tool := mkTool(`echo "$QUERY"`, nil)
	out, err := Run(context.Background(), tool, `{"query":"hi there"}`, nil, "")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.TrimSpace(out) != "hi there" {
		t.Errorf("got %q", out)
	}
}

func TestRun_ArgsJSONEnv(t *testing.T) {
	tool := mkTool(`echo "$ARGS_JSON"`, nil)
	out, err := Run(context.Background(), tool, `{"a":1,"b":"x"}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"a":1`) || !strings.Contains(out, `"b":"x"`) {
		t.Errorf("ARGS_JSON missing: %q", out)
	}
}

func TestRun_SecretInjection(t *testing.T) {
	tool := mkTool(`echo "tok=$API_TOKEN"`, []string{"API_TOKEN"})
	secrets := map[string]string{"API_TOKEN": "s3cr3t", "OTHER": "ignored"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "tok=") || strings.Contains(out, "s3cr3t") {
		// secret value should have been redacted
		t.Errorf("expected redacted secret, got %q", out)
	}
}

func TestRun_OnlyDeclaredSecretsExposed(t *testing.T) {
	tool := mkTool(`echo "other=$OTHER"`, []string{"API_TOKEN"})
	secrets := map[string]string{"API_TOKEN": "s3cr3t", "OTHER": "leaked"}
	out, err := Run(context.Background(), tool, `{}`, secrets, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "leaked") {
		t.Errorf("non-declared secret leaked: %q", out)
	}
}

func TestRun_Timeout(t *testing.T) {
	tool := mkTool(`sleep 5`, nil)
	tool.Timeout = 100 * time.Millisecond
	_, err := Run(context.Background(), tool, `{}`, nil, "")
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRun_Cwd(t *testing.T) {
	tool := mkTool(`pwd`, nil)
	cwd := t.TempDir()
	out, err := Run(context.Background(), tool, `{}`, nil, cwd)
	if err != nil {
		t.Fatal(err)
	}
	// macOS /var → /private/var symlink: just check it ends with the temp dir
	if !strings.Contains(out, cwd) && !strings.Contains("/private"+cwd, strings.TrimSpace(out)) {
		// loose check
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/usertools/ -run Run -v`
Expected: FAIL with undefined `Run`.

- [ ] **Step 3: Implement exec.go (no before/after yet — that's Task 4)**

```go
// internal/usertools/exec.go
package usertools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// Run executes a user tool. rawArgs is the JSON string from the LLM. secrets
// is the merged map of available secrets (only those listed in tool.Secrets
// are injected). defaultCwd is used when tool.Cwd is empty.
//
// The combined stdout+stderr is returned. Secret values are redacted from
// the output before returning. Errors from the subprocess (non-zero exit,
// timeout) are returned as Go errors and the partial output is still
// returned.
func Run(ctx context.Context, tool Tool, rawArgs string, secrets map[string]string, defaultCwd string) (string, error) {
	timeout := tool.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args, _ := parseArgsJSON(rawArgs)

	cmd := exec.CommandContext(runCtx, "bash", "-c", tool.Command)
	if tool.Cwd != "" {
		cmd.Dir = tool.Cwd
	} else {
		cmd.Dir = defaultCwd
	}

	cmd.Env = buildEnv(args, rawArgs, tool.Secrets, secrets)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()

	out := buf.String()

	// Redact secrets from output.
	var secretValues []string
	for _, name := range tool.Secrets {
		if v, ok := secrets[name]; ok && v != "" {
			secretValues = append(secretValues, v)
		}
	}
	out = Redact(out, secretValues)

	if runCtx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("usertools: %s: timed out after %s", tool.Name, timeout)
	}
	if runErr != nil {
		return out, fmt.Errorf("usertools: %s: %w", tool.Name, runErr)
	}
	return out, nil
}

// buildEnv composes the subprocess environment: parent PATH/HOME etc.,
// plus declared secrets, plus ARGS_JSON, plus uppercased flat scalar args.
func buildEnv(args map[string]any, rawArgs string, declaredSecrets []string, available map[string]string) []string {
	// Inherit parent env minus anything that looks like a secret or arg name —
	// we want a stable shell. We only filter the keys we are about to set.
	skip := map[string]struct{}{"ARGS_JSON": {}}
	for _, s := range declaredSecrets {
		skip[s] = struct{}{}
	}
	for k := range args {
		skip[strings.ToUpper(k)] = struct{}{}
	}
	env := []string{}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if _, drop := skip[kv[:eq]]; drop {
			continue
		}
		env = append(env, kv)
	}

	// Declared secrets only.
	for _, name := range declaredSecrets {
		if v, ok := available[name]; ok {
			env = append(env, name+"="+v)
		}
	}

	// ARGS_JSON for jq users.
	if rawArgs == "" {
		rawArgs = "{}"
	}
	env = append(env, "ARGS_JSON="+rawArgs)

	// Flat scalar args as UPPER_CASE env vars; complex values become JSON.
	for k, v := range args {
		key := strings.ToUpper(k)
		switch t := v.(type) {
		case string:
			env = append(env, key+"="+t)
		case bool:
			if t {
				env = append(env, key+"=true")
			} else {
				env = append(env, key+"=false")
			}
		case float64, int, int64:
			env = append(env, fmt.Sprintf("%s=%v", key, t))
		default:
			b, _ := json.Marshal(v)
			env = append(env, key+"="+string(b))
		}
	}
	return env
}

func parseArgsJSON(raw string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}, err
	}
	return m, nil
}
```

- [ ] **Step 4: Implement Redact (no-op stub for now, real impl in Task 5)**

```go
// internal/usertools/redact.go
package usertools

// Redact replaces each occurrence of any secretValue in s with
// "***REDACTED***". Empty secret values are ignored.
func Redact(s string, secretValues []string) string {
	for _, v := range secretValues {
		if v == "" {
			continue
		}
		s = stringsReplaceAll(s, v, "***REDACTED***")
	}
	return s
}

// indirection to avoid importing strings in redact.go just for one call;
// but keep it readable. Inline if you prefer:
func stringsReplaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	// stdlib strings.ReplaceAll
	return _replaceAll(s, old, new)
}
```

Actually simpler — drop the indirection:

```go
// internal/usertools/redact.go
package usertools

import "strings"

// Redact replaces each occurrence of any secretValue in s with
// "***REDACTED***". Empty secret values are ignored.
func Redact(s string, secretValues []string) string {
	for _, v := range secretValues {
		if v == "" {
			continue
		}
		s = strings.ReplaceAll(s, v, "***REDACTED***")
	}
	return s
}
```

(Use this second version. Delete the first sketch.)

- [ ] **Step 5: Run exec tests**

Run: `go test ./internal/usertools/ -v`
Expected: PASS — all tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/usertools/exec.go internal/usertools/exec_test.go internal/usertools/redact.go
git commit -m "feat(usertools): add Run with env injection, timeout, secret redaction"
```

---

## Task 4: Per-tool before/after hooks

**Files:**
- Modify: `internal/usertools/exec.go`
- Modify: `internal/usertools/exec_test.go`

- [ ] **Step 1: Write the failing test for `before` blocking**

```go
// append to internal/usertools/exec_test.go

func TestRun_BeforeBlocks(t *testing.T) {
	tool := mkTool(`echo should-not-run`, nil)
	tool.Before = `bash -c 'echo blocked >&2; exit 1'`
	out, err := Run(context.Background(), tool, `{}`, nil, "")
	if err == nil {
		t.Fatal("expected block error")
	}
	if strings.Contains(out, "should-not-run") {
		t.Errorf("command ran despite block: %q", out)
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("err should contain hook stderr: %v", err)
	}
}

func TestRun_BeforeRewritesArgs(t *testing.T) {
	tool := mkTool(`echo "q=$QUERY"`, nil)
	// before reads stdin args, returns a new args object on stdout
	tool.Before = `bash -c 'cat > /dev/null; echo "{\"query\":\"rewritten\"}"'`
	out, err := Run(context.Background(), tool, `{"query":"original"}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "q=rewritten") {
		t.Errorf("before did not rewrite args: %q", out)
	}
}

func TestRun_AfterRewritesOutput(t *testing.T) {
	tool := mkTool(`echo original`, nil)
	tool.After = `bash -c 'cat > /dev/null; echo transformed'`
	out, err := Run(context.Background(), tool, `{}`, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "transformed" {
		t.Errorf("after did not rewrite output: %q", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/usertools/ -run "Before|After" -v`
Expected: FAIL — hooks not implemented.

- [ ] **Step 3: Add hook helpers to exec.go**

Replace the body of `Run` with this version (keep `buildEnv` and `parseArgsJSON` unchanged):

```go
func Run(ctx context.Context, tool Tool, rawArgs string, secrets map[string]string, defaultCwd string) (string, error) {
	timeout := tool.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	// before hook: receives args JSON on stdin; on success its stdout (if
	// non-empty and valid JSON) replaces rawArgs. Non-zero exit blocks.
	if tool.Before != "" {
		newArgs, blockErr := runHook(ctx, tool.Before, defaultCwd, rawArgs, timeout)
		if blockErr != nil {
			return "", fmt.Errorf("usertools: %s: before hook: %w", tool.Name, blockErr)
		}
		if trimmed := strings.TrimSpace(newArgs); trimmed != "" {
			var probe map[string]any
			if json.Unmarshal([]byte(trimmed), &probe) == nil {
				rawArgs = trimmed
			}
		}
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args, _ := parseArgsJSON(rawArgs)

	cmd := exec.CommandContext(runCtx, "bash", "-c", tool.Command)
	if tool.Cwd != "" {
		cmd.Dir = tool.Cwd
	} else {
		cmd.Dir = defaultCwd
	}
	cmd.Env = buildEnv(args, rawArgs, tool.Secrets, secrets)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	runErr := cmd.Run()
	out := buf.String()

	// after hook: receives command output on stdin; its stdout replaces
	// the output. Non-zero exit logs but keeps original output.
	if tool.After != "" {
		newOut, hookErr := runHook(ctx, tool.After, defaultCwd, out, timeout)
		if hookErr == nil {
			out = newOut
		}
	}

	// Redact secrets after all transformations.
	var secretValues []string
	for _, name := range tool.Secrets {
		if v, ok := secrets[name]; ok && v != "" {
			secretValues = append(secretValues, v)
		}
	}
	out = Redact(out, secretValues)

	if runCtx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("usertools: %s: timed out after %s", tool.Name, timeout)
	}
	if runErr != nil {
		return out, fmt.Errorf("usertools: %s: %w", tool.Name, runErr)
	}
	return out, nil
}

// runHook runs a before/after shell command, piping `stdin` in and returning
// stdout. Stderr is included in the returned error on non-zero exit.
func runHook(ctx context.Context, command, cwd, stdin string, timeout time.Duration) (string, error) {
	hCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(hCtx, "bash", "-c", command)
	c.Dir = cwd
	c.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return stdout.String(), nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/usertools/ -v`
Expected: PASS — all hook tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/usertools/exec.go internal/usertools/exec_test.go
git commit -m "feat(usertools): add before/after per-tool hooks"
```

---

## Task 5: Wire usertools into persona + chat config

**Files:**
- Modify: `internal/persona/persona.go`
- Modify: `internal/chat/chat.go`

- [ ] **Step 1: Update persona.Load to accept user tools**

In `internal/persona/persona.go`, change `Load`'s signature:

```go
// Load reads <personasDir>/<name>.md, parses frontmatter, renders the body
// as a Go template with data, and assembles the tool list.
//
// userTools are merged after built-ins in the returned Persona.Tools.
func Load(personasDir, name string, data TemplateData, hasStore, noBash bool, userTools []ToolDef) (Persona, error) {
```

Inside the function, after the existing `tools` assembly, append:

```go
	tools = append(tools, userTools...)
```

- [ ] **Step 2: Fix existing callers and tests**

Search for callers and add a `nil` for the new parameter (callers will be updated in later tasks):

```bash
grep -rn "persona.Load(" --include="*.go"
```

Update each caller — for now just pass `nil` for `userTools`:
- `cmd/shell3/run.go:96` → add `, nil` before `)`
- any test caller in `internal/persona/persona_test.go`

- [ ] **Step 3: Run persona tests**

Run: `go test ./internal/persona/...`
Expected: PASS.

- [ ] **Step 4: Add fields to chat.Config**

In `internal/chat/chat.go`, after the existing imports, add:

```go
	"github.com/weatherjean/shell3/internal/usertools"
```

Update `Config`:

```go
type Config struct {
	LLM           LLMClient
	Hooks         *hooks.Runner
	Store         *store.Store
	Personality   persona.Persona
	WorkDir       string
	StatusLine    string
	ModeLabel     string
	Models        []string
	ModelSwitcher func(string)
	Truncate      bool
	Docs          string
	UserTools     map[string]usertools.Tool // by Name; nil OK
	Secrets       map[string]string         // merged .env + OS env; nil OK
}
```

- [ ] **Step 5: Build to verify compiles**

Run: `go build ./...`
Expected: success.

- [ ] **Step 6: Commit**

```bash
git add internal/persona/persona.go internal/chat/chat.go cmd/shell3/run.go internal/persona/persona_test.go
git commit -m "refactor(persona,chat): plumb user tools and secrets through config"
```

---

## Task 6: Dispatch user tools in turn.go

**Files:**
- Modify: `internal/chat/turn.go`
- Modify: `internal/chat/tools.go`
- Modify: `internal/chat/chat_test.go`

- [ ] **Step 1: Write the failing test**

Read `internal/chat/chat_test.go` to find the existing test pattern. Add a new test that runs an end-to-end turn through a fake LLM emitting a user-tool call, and verifies the dispatcher routes through `usertools.Run`. If the existing test file already mocks the LLM, follow the same pattern.

Sketch:

```go
// append to chat_test.go (adapt to the existing fake LLM helper)

func TestRunTurn_UserToolDispatched(t *testing.T) {
	// Build a fake LLM that emits one tool call to "say_hi" then ends.
	// Build chat.Config with UserTools containing a tool whose command is
	// `echo "hi $WHO"`. Run a turn. Assert the tool message in session
	// history contains "hi alice".
	// (full implementation follows the existing fake-LLM scaffolding)
	t.Skip("implement once existing test patterns are reviewed")
}
```

If the existing test scaffolding doesn't make this easy, replace this step with an exec-level integration test in `internal/usertools/` and rely on Task 10's smoke test for end-to-end coverage. Note the choice in the commit message.

- [ ] **Step 2: Add dispatcher helper**

In `internal/chat/tools.go`, add:

```go
import (
	// existing imports...
	"github.com/weatherjean/shell3/internal/usertools"
)

func dispatchUserTool(ctx context.Context, tool usertools.Tool, rawArgs string, secrets map[string]string, workDir string) string {
	out, err := usertools.Run(ctx, tool, rawArgs, secrets, workDir)
	if err != nil {
		if out != "" {
			return out + "\nerror: " + err.Error()
		}
		return "error: " + err.Error()
	}
	return out
}
```

- [ ] **Step 3: Wire dispatcher in turn.go**

In `internal/chat/turn.go`, replace the final `else` branch in `runTurn`'s tool-call switch (the one that calls `dispatchStore`) with:

```go
				} else if userTool, ok := cfg.UserTools[tc.Name]; ok {
					ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.Bold+"→ %s(%s)"+patchtui.Reset+"\n", tc.Name, tc.RawArgs)}
					out = dispatchUserTool(ctx, userTool, tc.RawArgs, cfg.Secrets, cfg.WorkDir)
					display := truncateOutput(out)
					if cfg.Truncate {
						display = out
					}
					ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n"}
				} else {
					ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.Bold+"→ %s(%s)"+patchtui.Reset+"\n", tc.Name, tc.RawArgs)}
					out = dispatchStore(tc.Name, tc.RawArgs, cfg.Store)
					display := truncateOutput(out)
					if cfg.Truncate {
						display = out
					}
					ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n"}
				}
```

The user-tool branch must come **before** the `dispatchStore` fallback so user tools take priority over the unknown-name error path.

- [ ] **Step 4: Build and run all tests**

```bash
go build ./...
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/turn.go internal/chat/tools.go internal/chat/chat_test.go
git commit -m "feat(chat): dispatch user-defined tools through usertools.Run"
```

---

## Task 7: Wire loading in cmd/shell3/run.go

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Load .env, OS env, and user tools before persona.Load**

In `cmd/shell3/run.go`, after `loadedSkills, _ := skills.LoadAll(...)` (around line 89), add:

```go
	// Load secrets: .env first, OS env wins on conflict.
	envPath := filepath.Join(cwd, ".shell3", ".env")
	dotEnv, dotEnvErr := usertools.LoadDotEnv(envPath)
	if dotEnvErr != nil {
		fmt.Fprintln(os.Stderr, "warning:", dotEnvErr)
	}
	secrets := map[string]string{}
	for k, v := range dotEnv {
		secrets[k] = v
	}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		secrets[kv[:eq]] = kv[eq+1:]
	}
	available := map[string]struct{}{}
	for k := range secrets {
		available[k] = struct{}{}
	}

	toolsDirs := []string{
		filepath.Join(homeDir, ".shell3", "tools"), // global
		filepath.Join(cwd, ".shell3", "tools"),     // project (overrides global)
	}
	loadedTools, toolWarnings, _ := usertools.LoadAll(toolsDirs, available)
	for _, w := range toolWarnings {
		fmt.Fprintln(os.Stderr, "user-tool warning:", w)
	}

	userToolDefs := make([]llm.ToolDefinition, 0, len(loadedTools))
	userToolMap := make(map[string]usertools.Tool, len(loadedTools))
	for _, ut := range loadedTools {
		userToolDefs = append(userToolDefs, llm.ToolDefinition{
			Name:        ut.Name,
			Description: ut.Description,
			Parameters:  ut.Parameters,
		})
		userToolMap[ut.Name] = ut
	}
```

Add imports: `"github.com/weatherjean/shell3/internal/usertools"`. Also add `"strings"` and `"os"` if not already present.

- [ ] **Step 2: Pass user tools to persona.Load**

Change the existing call:

```go
	pers, err := persona.Load(personasDir, personaName, personaData, st != nil, noBash, userToolDefs)
```

- [ ] **Step 3: Pass UserTools and Secrets in chat.Config**

Update the `chat.Config` literal:

```go
	cfg := chat.Config{
		LLM:           client,
		Hooks:         hookRunner,
		Store:         st,
		Personality:   pers,
		WorkDir:       cwd,
		StatusLine:    statusLine,
		ModeLabel:     pCfg.Name,
		Models:        models,
		ModelSwitcher: client.SetModel,
		Docs:          docsContent,
		UserTools:     userToolMap,
		Secrets:       secrets,
	}
```

- [ ] **Step 4: Build and run smoke test**

```bash
go build ./...
go test ./...
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(cmd): load .shell3/.env and tools/ at startup"
```

---

## Task 8: Update scaffold (tools dir, brave example, .env.example, gitignore)

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Write the failing test for new scaffold artifacts**

Add to `internal/scaffold/scaffold_test.go`:

```go
func TestInitProject_CreatesToolsDir(t *testing.T) {
	dir := setupCredsAndTempDir(t) // reuse existing helper if present
	if err := InitProject(dir, dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		".shell3/tools",
		".shell3/tools/brave_search.yaml",
		".shell3/.env.example",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
}

func TestInitProject_GitignoreContainsDotEnv(t *testing.T) {
	dir := setupCredsAndTempDir(t)
	if err := InitProject(dir, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".shell3", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), ".env") {
		t.Errorf("gitignore missing .env line:\n%s", data)
	}
}
```

If `setupCredsAndTempDir` does not exist, inline whatever the existing scaffold tests do to set up credentials (look at existing tests — they already do this).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/scaffold/...`
Expected: FAIL on the new tests.

- [ ] **Step 3: Update scaffold.go**

Update `defaultGitignore`:

```go
const defaultGitignore = `# shell3 runtime files — do not commit
shell3.db
memory.db
history.md
.env
`
```

Add new constants:

```go
const braveSearchTool = `name: brave_search
description: Web search via the Brave Search API. Returns top results as JSON. Set enabled to true after putting BRAVE_API_KEY in .shell3/.env.
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

const envExample = `# Copy this to .shell3/.env and fill in real values.
# .shell3/.env is gitignored. Do not commit secrets.
#
# BRAVE_API_KEY=your-key-here   # for tools/brave_search.yaml
`
```

In `initShell3Dir`, add `tools` to the `dirs` slice:

```go
	dirs := []string{
		shell3Dir,
		filepath.Join(shell3Dir, "skills"),
		filepath.Join(shell3Dir, "hooks"),
		filepath.Join(shell3Dir, "personas"),
		filepath.Join(shell3Dir, "tools"),
	}
```

In `files`, add:

```go
		filepath.Join(shell3Dir, ".env.example"):              envExample,
		filepath.Join(shell3Dir, "tools", "brave_search.yaml"): braveSearchTool,
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/scaffold/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go
git commit -m "feat(scaffold): add tools/ dir, brave_search example, .env.example, ignore .env"
```

---

## Task 9: Update shell3.md docs

**Files:**
- Modify: `cmd/shell3/shell3.md`

- [ ] **Step 1: Add User-Defined Tools section**

After the existing "Tools available to the model" table, add a new section:

````markdown
### User-Defined Tools

You can register your own tools by dropping YAML files into `.shell3/tools/` (project) or `~/.shell3/tools/` (global). Project tools override global ones on name collision.

Each tool file looks like this:

```yaml
name: brave_search           # required, [a-z][a-z0-9_]*, must not shadow built-ins
description: Web search…     # required, shown to the model
enabled: false               # required; tools default off
secrets: [BRAVE_API_KEY]     # optional; loaded from .shell3/.env or OS env
parameters:                  # required; JSON Schema (type must be object)
  type: object
  properties:
    query: {type: string, description: Search query}
  required: [query]
command: |                   # required; bash -c
  curl -sG https://api.example.com/search \
    -H "Authorization: Bearer $BRAVE_API_KEY" \
    --data-urlencode "q=$QUERY"
timeout: 15s                 # optional; default 30s
cwd: ""                      # optional; default = project workdir
before: ""                   # optional; bash -c hook, stdin = args JSON
after: ""                    # optional; bash -c hook, stdin = command output
```

**How args are passed to your command:**
- Each scalar arg is exported as an upper-cased env var. `query` → `$QUERY`, `count` → `$COUNT`.
- The full args object is in `$ARGS_JSON` for `jq` consumers.
- Complex values (arrays, objects) are JSON-encoded into their env var.

**Secrets:** Put `KEY=value` lines in `.shell3/.env` (file mode must be 0600 — `chmod 600 .shell3/.env`). Only the secrets listed in a tool's `secrets:` field are exposed to that tool. Secret values are scanned out of tool output and replaced with `***REDACTED***` before reaching the model.

**Hooks (per-tool, optional):**
- `before` — receives args JSON on stdin. Non-zero exit blocks the call (stderr becomes the block reason). Stdout (if valid JSON) replaces the args.
- `after` — receives command output on stdin. Stdout replaces the output. Non-zero exit is logged but the original output is kept.

The global `on_tool_call` / `on_tool_result` hooks (in your persona frontmatter) still run for user tools. Order: `on_tool_call` → tool `before` → command → tool `after` → secret redaction → `on_tool_result`.

**Validation:** Invalid tools are skipped at startup and a warning is printed to stderr. Reasons include: missing required fields, name shadowing a built-in (`bash`, `shell_interactive`, `memory_*`, `history_*`, `shell3_docs`), invalid name format, declared secret missing from environment.

**Example:** `.shell3/tools/brave_search.yaml` is created by `shell3 init` (disabled). Add `BRAVE_API_KEY=…` to `.shell3/.env`, set `enabled: true`, restart `shell3 code`.
````

- [ ] **Step 2: Add a row to the project config table**

Search for the existing `.shell3/config.yaml` table and add (or note in prose) that tools and `.env` live alongside it. No table change required if there's no top-level table for layout.

- [ ] **Step 3: Verify docs render correctly**

Run: `shell3 docs | head -50`
Expected: docs print without errors.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/shell3.md
git commit -m "docs(shell3): document user-defined tools, .env, secrets, before/after hooks"
```

---

## Task 10: End-to-end smoke test

**Files:**
- Create: `test/usertools_smoke_test.go` (or append to an existing test/)

- [ ] **Step 1: Write smoke test that exercises the loader pipeline**

Adapt to whatever harness `test/` already uses. The test should:

1. Create a tempdir with `.shell3/tools/echo.yaml`:
   ```yaml
   name: greet
   description: Say hi
   enabled: true
   parameters: {type: object, properties: {who: {type: string}}, required: [who]}
   command: 'echo "hello $WHO"'
   ```
2. Create `.shell3/.env` (mode 0600) with `IGNORED=value`.
3. Call `usertools.LoadDotEnv` + `usertools.LoadAll`.
4. Assert one tool is loaded, name `greet`.
5. Call `usertools.Run` with `{"who":"world"}` and assert output `hello world`.

```go
package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/usertools"
)

func TestUserTools_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	toolsDir := filepath.Join(dir, ".shell3", "tools")
	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		t.Fatal(err)
	}
	yaml := `name: greet
description: Say hi
enabled: true
parameters:
  type: object
  properties:
    who: {type: string}
  required: [who]
command: 'echo "hello $WHO"'
`
	if err := os.WriteFile(filepath.Join(toolsDir, "greet.yaml"), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
	envPath := filepath.Join(dir, ".shell3", ".env")
	if err := os.WriteFile(envPath, []byte("IGNORED=v\n"), 0600); err != nil {
		t.Fatal(err)
	}

	envMap, err := usertools.LoadDotEnv(envPath)
	if err != nil {
		t.Fatal(err)
	}
	avail := map[string]struct{}{}
	for k := range envMap {
		avail[k] = struct{}{}
	}
	tools, warnings, err := usertools.LoadAll([]string{toolsDir}, avail)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(tools) != 1 || tools[0].Name != "greet" {
		t.Fatalf("expected one greet tool, got %+v", tools)
	}

	out, err := usertools.Run(context.Background(), tools[0], `{"who":"world"}`, envMap, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("unexpected output: %q", out)
	}
}
```

- [ ] **Step 2: Run smoke test**

Run: `go test ./test/...`
Expected: PASS.

- [ ] **Step 3: Run full test suite**

Run: `go test ./...`
Expected: PASS.

- [ ] **Step 4: Manual smoke test against shell3 init**

```bash
mkdir -p /tmp/shell3-smoke && cd /tmp/shell3-smoke
go run ./cmd/shell3 init   # in the shell3 repo: adapt path
ls -la .shell3/tools .shell3/.env.example .shell3/.gitignore
cat .shell3/.gitignore   # expect .env in the list
cat .shell3/tools/brave_search.yaml | head -5
```

Expected: all three artifacts present, gitignore contains `.env`, brave_search.yaml has `enabled: false`.

- [ ] **Step 5: Commit**

```bash
git add test/usertools_smoke_test.go
git commit -m "test(usertools): end-to-end load + run smoke test"
```

---

## Task 11: Final review pass

- [ ] **Step 1: Verify all task changes integrate**

```bash
go build ./...
go test ./... -v
go vet ./...
```

Expected: all pass.

- [ ] **Step 2: Manual end-to-end test**

In a scratch dir with credentials configured:

```bash
mkdir -p .shell3/tools
cat > .shell3/.env <<'EOF'
GREETING=howdy
EOF
chmod 600 .shell3/.env
cat > .shell3/tools/greet.yaml <<'EOF'
name: greet
description: Greet someone using the configured GREETING.
enabled: true
secrets: [GREETING]
parameters:
  type: object
  properties:
    who: {type: string}
  required: [who]
command: 'echo "$GREETING, $WHO"'
EOF
shell3 init   # if not already initialized
shell3 code
# in TUI: /prompt — confirm `greet` shows in active tools
# ask the model: "use the greet tool with who=world"
```

Expected: tool fires, output reaches the model, `/prompt` lists it.

- [ ] **Step 3: Confirm secret redaction works manually**

Modify the greet tool's command to `echo "secret=$GREETING"`. Re-run. Verify the tool output (visible in TUI) shows `***REDACTED***` instead of `howdy`.

- [ ] **Step 4: No commit needed unless issues found**

If issues found, fix and commit per the standard pattern. Otherwise plan is complete.

---

## Self-Review Notes (for the agent reading this)

- Spec coverage: every concern raised in design — name/description/enabled/parameters/command/secrets/timeout/cwd/before/after, validation, env+stdin args, `.env` 0600 check, redaction, scaffold, gitignore, docs, brave example — has a task.
- Task 6's chat-level test is sketched as `t.Skip` if the existing chat test scaffolding doesn't make a fake-LLM test cheap; the smoke test in Task 10 covers the loader+exec path end-to-end either way.
- Type consistency: `Tool` always wraps `Spec`; `LoadAll` returns `[]Tool` and warnings; `Run` takes `Tool`; `chat.Config.UserTools` is `map[string]usertools.Tool`. Function names stable across tasks.
- No placeholders in code blocks. Every step that changes code shows the code.
