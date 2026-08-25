//go:build unix

package dash

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
)

// runsPageSize is how many runs one dash listing page shows.
const runsPageSize = 20

// Sources is what the dash reads. RunsRoot and ConfigDir are resolved per
// request (the store opens and closes inside each render call, the files
// explorer stats the config dir live), and the closures read live runtime
// state — all survive a /reload generation swap because nothing is cached here
// at startup.
type Sources struct {
	RunsRoot string
	// ConfigDir roots the read-only files explorer. Empty disables it.
	ConfigDir string
	// IndexHTML renders the front-page fragment for a given request token
	// (threaded into the fragment's own links).
	IndexHTML func(tok string) string
	// CronStatus / CronCosts back the per-job cron detail route. Nil = no
	// cron detail (the route 404s).
	CronStatus func() []cron.JobStatus
	CronCosts  func() map[string]runs.JobCost
}

// Server is the read-only dashboard: seven GET routes behind a token gate.
// It binds 127.0.0.1 only — reaching it from anywhere else is the exposure
// agent's business (a tunnel), never a wider bind.
type Server struct {
	port   int
	src    Sources
	log    applog.Logger
	tokens *TokenStore

	ln  net.Listener
	srv *http.Server
}

// New builds a server for 127.0.0.1:port (0 = ephemeral, for tests — the
// config's "0 = disabled" is decided by the caller, never here).
func New(port int, src Sources, log applog.Logger) *Server {
	if log == nil {
		log = applog.Noop{}
	}
	return &Server{port: port, src: src, log: log, tokens: NewTokenStore(nil)}
}

// Start binds and serves in the background. An error (port taken) is the
// caller's to report; the server holds no goroutine on failure.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(s.port))
	if err != nil {
		return fmt.Errorf("dash: %w", err)
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/runs", s.handleRuns)
	mux.HandleFunc("/runs/", s.handleReplay)
	mux.HandleFunc("/files", s.handleFiles)
	mux.HandleFunc("/file", s.handleFile)
	mux.HandleFunc("/joblog", s.handleJobLog)
	mux.HandleFunc("/cron", s.handleCron)
	s.srv = &http.Server{Handler: s.gate(mux)}
	go func() {
		if err := s.srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.log.Warn("dash server stopped", "err", err)
		}
	}()
	return nil
}

// Addr returns the actual bound address ("127.0.0.1:NNNN"); "" before Start.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Mint issues a fresh access token (TokenTTL).
func (s *Server) Mint() string { return s.tokens.Mint() }

// Close stops the listener. Idempotent; nil-safe before Start.
func (s *Server) Close() error {
	if s.srv == nil {
		return nil
	}
	return s.srv.Close()
}

// gate is the token middleware: every route, one rule, bare 403 on failure —
// no hint distinguishes a wrong token from a missing one.
func (s *Server) gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.tokens.Valid(r.URL.Query().Get("t")) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	frag := ""
	if s.src.IndexHTML != nil {
		frag = s.src.IndexHTML(r.URL.Query().Get("t"))
	}
	s.writePage(w, r, "shell3", frag)
}

// handleFiles lists a config-dir directory (?path=<rel>, default root).
func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	frag, ok := render.FilesListHTML(s.src.ConfigDir, r.URL.Query().Get("path"), tok)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.writePage(w, r, "files", frag)
}

// handleFile shows one config-dir file (?path=<rel>); credential files are
// redacted without being read, binary/oversized files flagged.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	frag, ok := render.FileViewHTML(s.src.ConfigDir, r.URL.Query().Get("path"), tok)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.writePage(w, r, "file", frag)
}

// handleJobLog shows a background job's captured output
// (?session=<sid>&id=<jid>).
func (s *Server) handleJobLog(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	frag, ok := render.JobLogHTML(s.src.RunsRoot, r.URL.Query().Get("session"), r.URL.Query().Get("id"), tok)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.writePage(w, r, "job", frag)
}

// handleCron shows one cron job's full detail (?name=<job>).
func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	if s.src.CronStatus == nil {
		http.NotFound(w, r)
		return
	}
	tok := r.URL.Query().Get("t")
	var costs map[string]runs.JobCost
	if s.src.CronCosts != nil {
		costs = s.src.CronCosts()
	}
	frag, ok := render.CronDetailHTML(s.src.CronStatus(), costs, r.URL.Query().Get("name"), tok)
	if !ok {
		http.NotFound(w, r)
		return
	}
	s.writePage(w, r, "cron", frag)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			http.NotFound(w, r)
			return
		}
		page = n
	}
	tok := r.URL.Query().Get("t")
	frag, total, err := render.RunsPageHTML(s.src.RunsRoot, page, runsPageSize, tok)
	if err != nil {
		s.log.Warn("dash runs render", "err", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	if frag == "" {
		frag = fmt.Sprintf("<section><h1>Runs</h1><p class=\"meta\">page %d is past the end (%d pages)</p></section>", page, total)
	}
	s.writePage(w, r, "runs", frag)
}

func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/runs/")
	page, err := render.RunReplayHTML(s.src.RunsRoot, id)
	if err != nil {
		// RunReplayHTML's errors are "invalid id" and "no such run"; a store
		// open failure is indistinguishable here and 404 is still the safe
		// answer for an authenticated read-only view.
		http.NotFound(w, r)
		return
	}
	// The replay is already a full page; graft the floating reload button in.
	page = strings.Replace(page, "</body>", reloadButton+"</body>", 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(page))
}

// reloadButton is the one dash-wide control: re-request the current URL
// (href="" preserves path and query, token included). No script, no polling.
const reloadButton = `<a class="dash-reload" href="" title="reload">&#8635; reload</a>
<style>.dash-reload{position:fixed;right:1rem;bottom:1rem;padding:.5rem .9rem;
border:1px solid #8884;border-radius:8px;background:#8881;color:inherit;
text-decoration:none;font-size:.9rem;backdrop-filter:blur(4px)}</style>
`

// writePage wraps a fragment in the dash page shell: nav carrying the
// request's own token, minimal CSS, the reload button.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request, title, frag string) {
	tok := r.URL.Query().Get("t")
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width,initial-scale=1\">\n")
	fmt.Fprintf(&b, "<title>%s · shell3</title>\n<style>%s</style>\n</head><body>\n", title, dashCSS)
	et := html.EscapeString(tok)
	fmt.Fprintf(&b, "<nav><a href=\"/?t=%s\">status</a> <a href=\"/runs?t=%s\">runs</a> <a href=\"/files?t=%s\">files</a></nav>\n", et, et, et)
	b.WriteString(frag)
	b.WriteString(reloadButton)
	b.WriteString("</body></html>\n")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// dashCSS mirrors the replay page's palette (render.runCSS) so the two read
// as one surface.
const dashCSS = `
:root{--bg:#fff;--fg:#1a1a1a;--dim:#666;--line:#e3e3e3;--pre:#f6f6f6;--acc:#2563eb}
@media (prefers-color-scheme:dark){:root{--bg:#141416;--fg:#e6e6e6;--dim:#9a9a9a;--line:#2c2c30;--pre:#1c1c20;--acc:#7ba7ff}}
*{box-sizing:border-box}
body{margin:0;padding:1rem 1rem 4rem;background:var(--bg);color:var(--fg);
font:15px/1.55 ui-sans-serif,-apple-system,Segoe UI,Roboto,sans-serif;max-width:60rem}
nav{border-bottom:1px solid var(--line);padding-bottom:.5rem;margin-bottom:1rem}
nav a{color:var(--acc);text-decoration:none;margin-right:1rem;font-weight:600}
h1{font-size:1.15rem;margin:.2rem 0 .6rem}
h2{font-size:1rem;margin:1rem 0 .4rem}
.meta{color:var(--dim);font-size:.85rem}
code{font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:.9em}
dl{display:grid;grid-template-columns:max-content 1fr;gap:.15rem .8rem;margin:.4rem 0}
dt{color:var(--dim)}dd{margin:0;overflow-wrap:anywhere}
table{border-collapse:collapse;width:100%;font-size:.9rem;margin:.4rem 0}
th{text-align:left;color:var(--dim);font-weight:600}
th,td{border-bottom:1px solid var(--line);padding:.3rem .6rem .3rem 0;vertical-align:top;overflow-wrap:anywhere}
a{color:var(--acc)}
section{margin-bottom:1.2rem}
ul{margin:.3rem 0;padding-left:1.2rem}
`
