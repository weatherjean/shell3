//go:build unix

package webui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileServer builds a Server with only the fields the file explorer touches,
// rooted at a temp config dir holding a few representative files.
func fileServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("agent.md", "# the agent\n")
	write(".env", "MAIN_API_KEY=super-secret-value\n")
	write("agents/researcher.md", "---\ndescription: digs\n---\n")

	return &Server{configDir: root}, root
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()
	var out T
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("bad JSON %q: %v", body, err)
	}
	return out
}

type listing struct {
	Path    string      `json:"path"`
	Parent  *string     `json:"parent"`
	Entries []fileEntry `json:"entries"`
}

func TestFilesListsRootWithDirsFirst(t *testing.T) {
	s, _ := fileServer(t)
	rec := httptest.NewRecorder()
	s.handleFiles(rec, httptest.NewRequest("GET", "/api/files?path=", nil))

	got := decode[listing](t, rec.Body.String())
	if got.Parent != nil {
		t.Errorf("root parent = %v, want null", *got.Parent)
	}
	if len(got.Entries) == 0 || !got.Entries[0].Dir {
		t.Fatalf("directories must sort first, got %+v", got.Entries)
	}

	byName := map[string]fileEntry{}
	for _, e := range got.Entries {
		byName[e.Name] = e
	}
	if !byName[".env"].Redacted {
		t.Error(".env must be flagged redacted in the listing")
	}
	if byName["agent.md"].Redacted {
		t.Error("an ordinary file must not be flagged redacted")
	}
	if byName["agents"].Path != "agents" {
		t.Errorf("entry path = %q, want agents", byName["agents"].Path)
	}
}

func TestFilesListsSubdirectoryWithParent(t *testing.T) {
	s, _ := fileServer(t)
	rec := httptest.NewRecorder()
	s.handleFiles(rec, httptest.NewRequest("GET", "/api/files?path=agents", nil))

	got := decode[listing](t, rec.Body.String())
	if got.Parent == nil || *got.Parent != "" {
		t.Errorf("parent = %v, want the root", got.Parent)
	}
	if len(got.Entries) != 1 || got.Entries[0].Path != "agents/researcher.md" {
		t.Errorf("entries = %+v, want agents/researcher.md", got.Entries)
	}
}

func TestFileContentReadsOrdinaryFile(t *testing.T) {
	s, _ := fileServer(t)
	rec := httptest.NewRecorder()
	s.handleFileContent(rec, httptest.NewRequest("GET", "/api/files/content?path=agent.md", nil))

	got := decode[map[string]any](t, rec.Body.String())
	if got["content"] != "# the agent\n" {
		t.Errorf("content = %q, want the file's text", got["content"])
	}
}

// The whole point of the redaction rule: the secret must never reach the
// browser, and the file must not even be opened.
func TestFileContentNeverReturnsCredentials(t *testing.T) {
	s, _ := fileServer(t)
	rec := httptest.NewRecorder()
	s.handleFileContent(rec, httptest.NewRequest("GET", "/api/files/content?path=.env", nil))

	body := rec.Body.String()
	if strings.Contains(body, "super-secret-value") {
		t.Fatal("the .env contents leaked to the browser")
	}
	if !strings.Contains(body, "credentials") {
		t.Errorf("response should explain the redaction, got %q", body)
	}
}

func TestIsCredentialFileCoversDotenvSiblings(t *testing.T) {
	for _, name := range []string{".env", ".ENV", ".env.local", ".env.production"} {
		if !isCredentialFile(name) {
			t.Errorf("isCredentialFile(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"env", "agent.md", "environment.md", "a.env"} {
		if isCredentialFile(name) {
			t.Errorf("isCredentialFile(%q) = true, want false", name)
		}
	}
}

// Traversal must be clamped at the config root, however it is spelled.
func TestFilesRejectsEscapes(t *testing.T) {
	s, root := fileServer(t)
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("not yours"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(outside)

	for _, path := range []string{"../outside.txt", "../../etc/passwd", "agents/../../outside.txt"} {
		rec := httptest.NewRecorder()
		s.handleFileContent(rec,
			httptest.NewRequest("GET", "/api/files/content?path="+path, nil))
		if body := rec.Body.String(); strings.Contains(body, "not yours") {
			t.Errorf("path %q escaped the config root", path)
		}
	}
}

// A symlink pointing outside the root must not become a read primitive.
func TestFilesRejectsSymlinkEscape(t *testing.T) {
	s, root := fileServer(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("leaked"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	rec := httptest.NewRecorder()
	s.handleFileContent(rec,
		httptest.NewRequest("GET", "/api/files/content?path=link.txt", nil))
	if strings.Contains(rec.Body.String(), "leaked") {
		t.Error("a symlink read outside the config root")
	}
}

func TestFileContentRejectsDirectory(t *testing.T) {
	s, _ := fileServer(t)
	rec := httptest.NewRecorder()
	s.handleFileContent(rec, httptest.NewRequest("GET", "/api/files/content?path=agents", nil))
	if rec.Code != 400 {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFilesRejectsMissingPath(t *testing.T) {
	s, _ := fileServer(t)
	rec := httptest.NewRecorder()
	s.handleFiles(rec, httptest.NewRequest("GET", "/api/files?path=nope", nil))
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// Empty collections must marshal as [] — the browser iterates them without a
// null guard, so a nil slice would break the Status view on a bare config.
func TestStatusCollectionsAreNeverNull(t *testing.T) {
	resp := statusResp{
		Warnings:  []string{},
		Params:    []paramResp{},
		Subagents: []subagentResp{},
		Projects:  []projectResp{},
		Skills:    []skillResp{},
		Cron:      []cronResp{},
		MCP:       []mcpResp{},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "null") {
		t.Errorf("status JSON contains null: %s", b)
	}
}
