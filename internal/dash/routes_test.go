//go:build unix

package dash_test

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/dash"
)

// startFullServer wires the files explorer and cron sources so the new routes
// have something to serve.
func startFullServer(t *testing.T, root, configDir string, statuses []cron.JobStatus) *dash.Server {
	t.Helper()
	s := dash.New(0, dash.Sources{
		RunsRoot:   root,
		ConfigDir:  configDir,
		IndexHTML:  func(string) string { return "<section><h1>idx</h1></section>" },
		CronStatus: func() []cron.JobStatus { return statuses },
	}, applog.Noop{})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestFilesRoutes(t *testing.T) {
	cfg := t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg, "shell3.sh"), []byte("# kit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, ".env"), []byte("SECRET=leak-me-42"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startFullServer(t, t.TempDir(), cfg, nil)
	base := "http://" + s.Addr()
	tok := s.Mint()

	// Listing needs a token.
	if code, _ := get(t, base+"/files"); code != http.StatusForbidden {
		t.Fatalf("/files without token: %d, want 403", code)
	}
	code, body := get(t, base+"/files?t="+tok)
	if code != http.StatusOK {
		t.Fatalf("/files: %d", code)
	}
	if !contains(body, "shell3.sh") {
		t.Errorf("listing missing kit: %s", body)
	}

	// SECURITY: reading .env over HTTP must not leak the secret.
	code, body = get(t, base+"/file?path=.env&t="+tok)
	if code != http.StatusOK {
		t.Fatalf("/file .env: %d", code)
	}
	if contains(body, "leak-me-42") {
		t.Fatalf("SECRET LEAKED over HTTP: %s", body)
	}

	// SECURITY: traversal is refused.
	code, _ = get(t, base+"/file?path="+url.QueryEscape("../../etc/passwd")+"&t="+tok)
	if code != http.StatusNotFound {
		t.Errorf("traversal: %d, want 404", code)
	}
}

func TestCronDetailRoute(t *testing.T) {
	statuses := []cron.JobStatus{{Name: "nightly", Schedule: "0 3 * * *", Agent: "manager", Prompt: "sync things"}}
	s := startFullServer(t, t.TempDir(), t.TempDir(), statuses)
	base := "http://" + s.Addr()
	tok := s.Mint()

	code, body := get(t, base+"/cron?name=nightly&t="+tok)
	if code != http.StatusOK {
		t.Fatalf("/cron: %d", code)
	}
	if !contains(body, "nightly") || !contains(body, "sync things") {
		t.Errorf("cron detail missing fields: %s", body)
	}
	// Unknown job 404s.
	if code, _ := get(t, base+"/cron?name=nope&t="+tok); code != http.StatusNotFound {
		t.Errorf("unknown cron: %d, want 404", code)
	}
}

func TestJobLogRoute(t *testing.T) {
	root := t.TempDir()
	logDir := filepath.Join(root, "sess1", "jobs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "bg1.log"), []byte("job ran ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := startFullServer(t, root, t.TempDir(), nil)
	base := "http://" + s.Addr()
	tok := s.Mint()

	code, body := get(t, base+"/joblog?session=sess1&id=bg1&t="+tok)
	if code != http.StatusOK {
		t.Fatalf("/joblog: %d", code)
	}
	if !contains(body, "job ran ok") {
		t.Errorf("job log missing output: %s", body)
	}
	// SECURITY: a traversal in session/id is refused.
	if code, _ := get(t, base+"/joblog?session="+url.QueryEscape("../../etc")+"&id=passwd&t="+tok); code != http.StatusNotFound {
		t.Errorf("joblog traversal: %d, want 404", code)
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
