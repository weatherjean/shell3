//go:build unix

package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/shell3"
	"github.com/weatherjean/shell3/pkg/shell3/shell3test"
)

// newFilesServer builds a dashboard server rooted at a temp config dir seeded
// with a representative config layout (shell3.lua, a secrets .env, a skills
// subdir), and returns the server plus a signed-initData query string.
func newFilesServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	const token = "test-bot-token"
	const chatID int64 = 8701499393

	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "shell3.lua"), "shell3.model{ name = \"code\" }\n")
	mustWrite(t, filepath.Join(dir, ".env"), "OPENAI_API_KEY=sk-supersecret\n")
	if err := os.Mkdir(filepath.Join(dir, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "skills", "history.md"), "# history skill\n")

	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, token, chatID)
	srv.SetConfigDir(dir)
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)
	return srv, signed, dir
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFiles_AuthRequired(t *testing.T) {
	srv, _, _ := newFilesServer(t)
	for _, p := range []string{"/api/files", "/api/file?path=shell3.lua"} {
		rr := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s without auth: want 401, got %d", p, rr.Code)
		}
	}
}

func TestFiles_ListRootDirsFirst(t *testing.T) {
	srv, signed, _ := newFilesServer(t)
	rr := get(t, srv, signed, "/api/files")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	// The skills/ directory must be listed and ordered before files.
	if !strings.Contains(body, `"name":"skills"`) || !strings.Contains(body, `"dir":true`) {
		t.Fatalf("listing missing skills dir: %s", body)
	}
	if iDir, iFile := strings.Index(body, `"name":"skills"`), strings.Index(body, `"name":"shell3.lua"`); iDir > iFile {
		t.Fatalf("dirs should sort before files: %s", body)
	}
	// The .env is listed but flagged redacted (size of contents not its concern).
	if !strings.Contains(body, `"name":".env"`) || !strings.Contains(body, `"redacted":true`) {
		t.Fatalf("listing should flag .env redacted: %s", body)
	}
}

func TestFiles_ReadNormalFile(t *testing.T) {
	srv, signed, _ := newFilesServer(t)
	rr := get(t, srv, signed, "/api/file?path=shell3.lua")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "shell3.model") {
		t.Fatalf("file content missing: %s", rr.Body.String())
	}
}

func TestFiles_ReadCredentialFileRedacted(t *testing.T) {
	srv, signed, _ := newFilesServer(t)
	rr := get(t, srv, signed, "/api/file?path=.env")
	if rr.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if strings.Contains(body, "supersecret") {
		t.Fatalf("SECRET LEAKED in .env read: %s", body)
	}
	if !strings.Contains(body, `"redacted":true`) {
		t.Fatalf(".env read should be marked redacted: %s", body)
	}
}

func TestFiles_TraversalBlocked(t *testing.T) {
	srv, signed, dir := newFilesServer(t)
	// A secret one level above the config root must be unreachable.
	mustWrite(t, filepath.Join(filepath.Dir(dir), "outside-secret.txt"), "TOP SECRET")
	for _, p := range []string{
		"/api/file?path=../outside-secret.txt",
		"/api/file?path=../../etc/passwd",
		"/api/files?path=..",
	} {
		rr := get(t, srv, signed, p)
		if rr.Code == http.StatusOK && strings.Contains(rr.Body.String(), "TOP SECRET") {
			t.Fatalf("%s ESCAPED the config root: %s", p, rr.Body.String())
		}
	}
}

func TestFiles_NoConfigDirEmptyListing(t *testing.T) {
	const token = "test-bot-token"
	const chatID int64 = 8701499393
	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(rt, sess, token, chatID) // no SetConfigDir
	signed := signInitData(t, token, `{"id":8701499393,"first_name":"T"}`)
	rr := get(t, srv, signed, "/api/files")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"entries":[]`) {
		t.Fatalf("no-config-dir listing: got %d %q", rr.Code, rr.Body.String())
	}
}

func TestStatic_VendoredAssetServed(t *testing.T) {
	srv, _, _ := newFilesServer(t)
	// Public (no initData) and embedded from static/vendor.
	rr := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/static/vendor/highlight/highlight.min.js", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("vendored asset: want 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "Highlight.js") {
		t.Fatalf("vendored asset body does not look like highlight.js")
	}
}

// get issues an authenticated GET against the server's handler.
func get(t *testing.T, srv *Server, signed, path string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Init-Data", signed)
	srv.Handler().ServeHTTP(rr, req)
	return rr
}
