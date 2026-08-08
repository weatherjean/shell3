//go:build unix

package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Server is the web front-end: it serves the single-page app and every API it
// talks to, and it owns the conversational orchestration — the single turn
// slot, the thread→session map, the command gate, and the completion funnel's
// delivery half.
//
// One browser, one agent. The turn gate is global rather than per-thread
// because the runtime runs one main-agent turn at a time by design; a cron
// result and a typed message contend for the same slot.
type Server struct {
	rt        *shell3.Runtime
	workDir   string
	configDir string
	version   string
	started   time.Time
	log       applog.Logger

	hub     *hub
	asks    *asks
	threads *threadIndex
	tasks   chan task
	// push delivers notifications to browsers that are not open. nil when the
	// keypair could not be created, which disables push and nothing else.
	push *pusher

	// media is rebuilt on reload; guarded by mu.
	media *media.Clients
	// tts caches spoken replies on disk; rebuilt with media on reload.
	tts *ttsCache
	// cronSource/runCron come from the live scheduler and are swapped by
	// SetCronSource on every reload (guarded by mu): a cron/ file added
	// after startup must start firing once /reload arms it.
	cronSource func() []cron.JobStatus
	runCron    func(name string) error
	// reloadHook runs after every successful config reload — the host wires
	// re-arming work that lives outside this package here (the cron
	// scheduler). Its error is appended to the reload's reply.
	reloadHook func() error
	// applyReloadFn performs the reload; s.applyReload in production, stubbed
	// in tests (whose runtimes have no config dir to re-read).
	applyReloadFn func() (string, bool)
	// pendingReload is set by the agent's reload tool mid-turn and applied
	// once the turn ends (a reload cannot run under a busy turn).
	pendingReload bool

	mu         sync.Mutex
	live       map[string]*shell3.Session // thread id → session
	order      []string                   // thread ids, oldest first
	recentSess *shell3.Session
	// recent is the notification replay buffer (see publishNotification).
	recent []notification
	// usage is the last turn's token count, for the Status view. Only the most
	// recent turn: a cumulative total across a process lifetime says little,
	// while "that turn cost 40k prompt tokens" is actionable.
	usage      *usageResp
	turnActive bool
	cancelTurn context.CancelFunc
	// attachable is the running turn's chunk broker, published so a client
	// that lost its connection can re-attach (see attach.go).
	attachable       *turnBroker
	attachableThread string
	// turnSession is the session the running turn belongs to, tracked apart
	// from recentSess so a concurrent read request cannot misdirect /api/stop.
	turnSession *shell3.Session
	notifySeq   int
	// notifySeen is the seq of the newest notification the user has seen
	// (opening the bell marks everything seen); replayed entries at or below
	// it come back read, so a reload does not resurrect the badge.
	notifySeen int

	// sessions are the logged-in browsers; auth holds the login route's
	// failure backoff and TOTP replay guard. Both outlive a /reload: a config
	// change must not log anyone out unless the password itself changed.
	sessions *sessionStore
	auth     *authGate
}

// Options configures a Server. Everything except Runtime has a sane zero.
type Options struct {
	Runtime   *shell3.Runtime
	WorkDir   string // where the agent's shell runs
	ConfigDir string // the config root the Files view exposes
	Version   string
	// StateDir holds webui's own on-disk state (browser login sessions, push
	// keys) that isn't part of the runs store. Defaults to
	// <ConfigDir>/.shell3_project.
	StateDir string
	// Store, if set, backs the thread index directly instead of resolving it
	// from Runtime on every call — for tests, where the runtime has no config
	// Parts and so no store of its own.
	Store *runs.Store
}

// New builds the front-end and wires it into the runtime: it installs itself
// as the completion host, so finished background work reaches the browser.
func New(opts Options) (*Server, error) {
	if opts.Runtime == nil {
		return nil, fmt.Errorf("webui: nil runtime")
	}
	stateDir := opts.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(opts.ConfigDir, ".shell3_project")
	}

	rt := opts.Runtime
	storeFn := func() *runs.Store {
		if opts.Store != nil {
			return opts.Store
		}
		if parts := rt.Parts(); parts != nil {
			return parts.Store()
		}
		return nil
	}
	log := runtimeLogger(opts.Runtime)
	threads := newThreadIndex(storeFn, log.Warn)

	sessions, err := newSessionStore(stateDir)
	if err != nil {
		return nil, fmt.Errorf("webui: session store: %w", err)
	}

	h := newHub()
	s := &Server{
		rt:        opts.Runtime,
		workDir:   opts.WorkDir,
		configDir: opts.ConfigDir,
		version:   opts.Version,
		started:   time.Now(),
		log:       log,
		hub:       h,
		asks:      newAsks(h),
		threads:   threads,
		tasks:     make(chan task, 64),
		live:      make(map[string]*shell3.Session),
		sessions:  sessions,
		auth:      &authGate{},
	}

	// Push is a bonus channel: if its keys cannot be written, notifications
	// still reach an open tab, so a failure here is a warning not an error.
	if push, err := newPusher(stateDir); err != nil {
		s.log.Warn("webui: web push unavailable", "error", err.Error())
	} else {
		s.push = push
	}

	s.applyReloadFn = s.applyReload
	s.resync()
	opts.Runtime.SetCompletionHost(s)
	return s, nil
}

// Start runs the background loops: the worker that executes out-of-turn work
// (cron results, job completions the notifier chose to wake on), and the
// forwarder that pushes live job progress to the browser. Returns when ctx
// ends.
func (s *Server) Start(ctx context.Context) {
	go s.watchJobs(ctx)
	s.runWorker(ctx)
}

// Close is a no-op: the thread index no longer owns a file handle (it writes
// through the runs store, which the runtime closes). Kept so callers don't
// need to know that changed.
func (s *Server) Close() error { return nil }

// runtimeLogger returns the runtime's logger, or a discard logger for a
// runtime built without config Parts (the test harness).
func runtimeLogger(rt *shell3.Runtime) applog.Logger {
	if parts := rt.Parts(); parts != nil {
		return parts.Log()
	}
	return applog.Noop{}
}

// resync rebuilds the media clients and re-registers the session decorator
// against the current config. Called at startup and after every reload, since
// a reload rebuilds session configs and drops registered host tools.
//
// A runtime without config Parts (the test harness) has no media to wire.
func (s *Server) resync() {
	parts := s.rt.Parts()
	if parts == nil {
		return
	}
	clients := media.New(parts.MediaConfig(), parts.EnsureProxy)

	// The cache key includes the voice configuration, so changing the voice in
	// shell3.yaml produces new audio instead of replaying the old voice.
	var cache *ttsCache
	if clients.Speak != nil {
		fingerprint := "tts"
		if tts := parts.MediaConfig().TTS(); tts != nil {
			fingerprint = tts.ModelRef + "|" + tts.Voice + "|" + tts.Format
		}
		speak := clients.Speak
		cache = newTTSCache(func(text string) (media.Speech, error) {
			return speak(context.Background(), text)
		}, fingerprint)
	}

	s.mu.Lock()
	s.media = clients
	s.tts = cache
	s.mu.Unlock()

	s.rt.SetSessionDecorator(func(sess *shell3.Session) {
		if err := media.RegisterImageTool(sess, clients); err != nil {
			s.log.Error("webui: image tool", err)
		}
		if err := RegisterSendFileTool(sess, s.workDir, s.configDir); err != nil {
			s.log.Error("webui: send_file tool", err)
		}
		if err := RegisterReloadTool(sess, s.queueReload); err != nil {
			s.log.Error("webui: reload tool", err)
		}
		if err := RegisterStatusTool(sess, s.statusDigest); err != nil {
			s.log.Error("webui: status tool", err)
		}
	})
}

// route is one registered endpoint and, crucially, whether it can be reached
// without logging in. Routes are declared in a table rather than registered
// inline so that authentication is not something a new endpoint can forget:
// adding one means stating its status here, and auth_test walks this same table
// asserting every private route refuses an unauthenticated request.
type route struct {
	pattern string
	handler http.HandlerFunc
	// public routes are reachable with no session. Only two things qualify: the
	// login route itself, and the static shell that draws the login screen.
	public bool
}

// routes is the whole HTTP surface. Everything is private unless it has to be
// public for login to be possible at all.
func (s *Server) routes() []route {
	return []route{
		// Getting in. Public by necessity — this is where a session comes from.
		{pattern: "/api/login", handler: s.handleLogin, public: true},
		{pattern: "/api/logout", handler: s.handleLogout},

		// Conversation
		{pattern: "/api/chat", handler: s.handleChat},
		{pattern: "/api/chat/stream", handler: s.handleChatAttach},
		{pattern: "/api/events", handler: s.handleEvents},
		{pattern: "/api/asks/", handler: s.handleAskAnswer},
		{pattern: "/api/stop", handler: s.handleStop},

		// The bell's read state, kept server-side so a page reload does not
		// resurrect a badge the user already cleared.
		{pattern: "/api/notifications/seen", handler: s.handleNoticesSeen},
		{pattern: "/api/notifications/dismiss", handler: s.handleNoticeDismiss},

		// Conversations
		{pattern: "/api/threads", handler: s.handleThreads},
		{pattern: "/api/threads/", handler: s.handleThread},

		// Introspection
		{pattern: "/api/capabilities", handler: s.handleCapabilities},
		{pattern: "/api/status", handler: s.handleStatus},
		{pattern: "/api/files", handler: s.handleFiles},
		{pattern: "/api/files/content", handler: s.handleFileContent},
		{pattern: "/api/jobs", handler: s.handleJobs},
		{pattern: "/api/jobs/", handler: s.routeJobs},
		{pattern: "/api/cron", handler: s.handleCron},
		{pattern: "/api/cron/", handler: s.routeCron},
		{pattern: "/api/runs", handler: s.handleRuns},
		{pattern: "/api/runs/", handler: s.routeRuns},
		{pattern: "/api/reload", handler: s.handleReload},

		// Push notifications, for when the tab is closed.
		{pattern: "/api/push", handler: s.handlePushKey},
		{pattern: "/api/push/subscribe", handler: s.handlePushSubscribe},
		{pattern: "/api/push/test", handler: s.handlePushTest},

		// Voice
		{pattern: "/api/stt", handler: s.handleSTT},
		{pattern: "/api/tts", handler: s.handleTTS},

		// Uploads and generated images, so a reply can render them inline — and
		// so they stay findable after the message scrolls away. Private: these
		// are the operator's files.
		{pattern: "/api/media", handler: s.handleMediaList},
		{pattern: "/api/media/", handler: s.handleMediaFile},

		// The service worker. An exact pattern beats the catch-all below, which
		// is how it stays gated while the rest of the shell is public: it is
		// registered by the app after logging in, so the cookie is there.
		{pattern: "/sw.js", handler: appHandler().ServeHTTP},

		// The app itself and its assets: public, because it has to render the
		// login screen for someone with no session. It carries no secrets —
		// this bundle is the published open-source front-end — and every byte of
		// data it goes on to display comes from the private routes above.
		{pattern: "/", handler: appHandler().ServeHTTP, public: true},
	}
}

// Handler returns the mux serving the app and its API, with every private route
// behind the session gate.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	for _, rt := range s.routes() {
		handler := rt.handler
		if !rt.public {
			handler = s.requireSession(handler)
		}
		mux.HandleFunc(rt.pattern, handler)
	}
	return mux
}

// handleEvents is the server-push stream: notifications and approval requests.
// Still-parked approvals are replayed on connect, so reloading the page never
// strands a turn waiting on a decision nobody can see.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, cancel := s.hub.subscribe()
	defer cancel()

	// Subscribe first, then replay: anything arriving in between is delivered
	// twice (harmless — the client keys on id) rather than missed.
	//
	// Notifications are replayed too, so reopening the tab shows the background
	// work that finished while it was closed.
	for _, notice := range s.recentNotices() {
		if data, err := json.Marshal(notice); err == nil {
			fmt.Fprintf(w, "event: notification\ndata: %s\n\n", data)
		}
	}
	for _, pending := range s.asks.snapshot() {
		if data, err := json.Marshal(pending); err == nil {
			fmt.Fprintf(w, "event: ask\ndata: %s\n\n", data)
		}
	}
	flusher.Flush()

	// A periodic comment keeps intermediaries from closing an idle stream.
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleAskAnswer records the browser's Allow/Deny for a parked command.
func (s *Server) handleAskAnswer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/asks/")
	if id == "" {
		http.Error(w, "missing ask id", http.StatusBadRequest)
		return
	}
	var body struct {
		Allow bool `json:"allow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.asks.Answer(id, body.Allow) {
		http.Error(w, "no such request", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"recorded": true})
}
