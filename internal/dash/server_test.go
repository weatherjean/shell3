//go:build unix

package dash_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/dash"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// seedRun writes one session with messages and returns (root, id).
func seedRun(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	id, err := st.NewSession(runs.Meta{Model: "test"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello <script>alert(1)</script>"},
		{Role: llm.RoleAssistant, Content: "hi there"},
	}
	for _, m := range msgs {
		if err := st.AppendMessage(id, m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return root, id
}

func startServer(t *testing.T, root string) *dash.Server {
	t.Helper()
	s := dash.New(0, dash.Sources{
		RunsRoot:  root,
		IndexHTML: func(string) string { return "<section><h1>index-fragment</h1></section>" },
	}, applog.Noop{})
	if err := s.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestTokenGate(t *testing.T) {
	root, _ := seedRun(t)
	s := startServer(t, root)
	base := "http://" + s.Addr()

	if code, _ := get(t, base+"/"); code != http.StatusForbidden {
		t.Fatalf("no token: code %d, want 403", code)
	}
	if code, _ := get(t, base+"/?t=wrong"); code != http.StatusForbidden {
		t.Fatalf("bad token: code %d, want 403", code)
	}
	tok := s.Mint()
	code, body := get(t, base+"/?t="+tok)
	if code != http.StatusOK {
		t.Fatalf("good token: code %d, want 200", code)
	}
	if !strings.Contains(body, "index-fragment") {
		t.Error("index fragment missing from page")
	}
	if !strings.Contains(body, "href=\"\"") {
		t.Error("reload button missing")
	}
	if !strings.Contains(body, "/runs?t="+tok) {
		t.Error("nav link does not carry the token")
	}
}

func TestRunsPages(t *testing.T) {
	root, id := seedRun(t)
	s := startServer(t, root)
	base := "http://" + s.Addr()
	tok := s.Mint()

	code, body := get(t, base+"/runs?t="+tok)
	if code != http.StatusOK || !strings.Contains(body, "/runs/"+id+"?t="+tok) {
		t.Fatalf("runs listing: code %d body missing link", code)
	}

	code, body = get(t, base+"/runs/"+id+"?t="+tok)
	if code != http.StatusOK {
		t.Fatalf("replay: code %d, want 200", code)
	}
	if strings.Contains(body, "<script>alert") {
		t.Fatal("replay did not escape tool text")
	}
	if !strings.Contains(body, "hi there") {
		t.Error("replay missing message text")
	}
	if !strings.Contains(body, "href=\"\"") {
		t.Error("replay page missing reload button")
	}

	if code, _ := get(t, base+"/runs/nope-such-run?t="+tok); code != http.StatusNotFound {
		t.Errorf("missing run: code %d, want 404", code)
	}
	if code, _ := get(t, base+"/runs/"+id); code != http.StatusForbidden {
		t.Errorf("replay without token: code %d, want 403", code)
	}
}

func TestMethodGuard(t *testing.T) {
	root, _ := seedRun(t)
	s := startServer(t, root)
	tok := s.Mint()
	resp, err := http.Post("http://"+s.Addr()+"/?t="+tok, "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST: code %d, want 405", resp.StatusCode)
	}
}

func TestUnknownPath(t *testing.T) {
	root, _ := seedRun(t)
	s := startServer(t, root)
	tok := s.Mint()
	if code, _ := get(t, "http://"+s.Addr()+"/nope?t="+tok); code != http.StatusNotFound {
		t.Fatalf("unknown path: code %d, want 404", code)
	}
}

// Traversal-shaped run ids never reach the store: raw ".." (or an encoded
// slash) 404s — or is path-cleaned by the mux into a redirect that still
// never serves a page. Nothing outside the runs table is reachable by id.
func TestReplayRejectsTraversalIDs(t *testing.T) {
	root, _ := seedRun(t)
	s := startServer(t, root)
	tok := s.Mint()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	for _, id := range []string{"..", "%2e%2e", "a%2Fb", "..%2F..%2Fetc"} {
		resp, err := client.Get("http://" + s.Addr() + "/runs/" + id + "?t=" + tok)
		if err != nil {
			t.Fatalf("GET %q: %v", id, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("traversal id %q served a page (code 200)", id)
		}
	}
}
