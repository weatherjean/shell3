//go:build unix

package webui

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/weatherjean/shell3/internal/strutil"
)

// Web push: notifications that arrive when the tab is closed, or the phone is
// in a pocket. Everything the bell shows also goes out this way.
//
// Two things make it work: a VAPID keypair identifying this install (generated
// once, kept in the config dir), and a per-browser subscription the user grants
// by allowing notifications. Push requires a secure context, so it works on
// localhost and over https exposure, but not over plain http to another host.

// pushKeysFile stores the VAPID keypair beside the runs data — machine state,
// not configuration a user edits.
const pushKeysFile = "web_push_keys.json"

type pushKeys struct {
	Public  string `json:"public"`
	Private string `json:"private"`
}

// subscription is one browser's push endpoint.
type subscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
	// Added is when the browser subscribed, for pruning stale entries.
	Added string `json:"added,omitempty"`
}

// pusher holds the keypair and the live subscriptions.
type pusher struct {
	path string // where subscriptions are persisted
	keys pushKeys

	mu   sync.Mutex
	subs map[string]subscription // endpoint → subscription
}

// newPusher loads (or creates) the VAPID keypair and any stored subscriptions.
// A failure here disables push rather than the server: notifications still
// reach an open tab.
func newPusher(stateDir string) (*pusher, error) {
	keys, err := loadOrCreateKeys(filepath.Join(stateDir, pushKeysFile))
	if err != nil {
		return nil, err
	}
	p := &pusher{
		path: filepath.Join(stateDir, "web_push_subs.json"),
		keys: keys,
		subs: map[string]subscription{},
	}
	p.load()
	return p, nil
}

func loadOrCreateKeys(path string) (pushKeys, error) {
	if data, err := os.ReadFile(path); err == nil {
		var keys pushKeys
		if json.Unmarshal(data, &keys) == nil && keys.Public != "" && keys.Private != "" {
			return keys, nil
		}
	} else if !os.IsNotExist(err) {
		return pushKeys{}, err
	}

	private, public, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return pushKeys{}, err
	}
	keys := pushKeys{Public: public, Private: private}

	data, err := json.Marshal(keys)
	if err != nil {
		return pushKeys{}, err
	}
	// The private key signs pushes for this install; keep it owner-only.
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return pushKeys{}, err
	}
	return keys, nil
}

func (p *pusher) load() {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	var stored []subscription
	if json.Unmarshal(data, &stored) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, sub := range stored {
		if sub.Endpoint != "" {
			p.subs[sub.Endpoint] = sub
		}
	}
}

// saveLocked persists the subscription set. Caller holds p.mu.
func (p *pusher) saveLocked() {
	list := make([]subscription, 0, len(p.subs))
	for _, sub := range p.subs {
		list = append(list, sub)
	}
	if data, err := json.Marshal(list); err == nil {
		_ = os.WriteFile(p.path, data, 0o600)
	}
}

func (p *pusher) add(sub subscription) {
	if sub.Endpoint == "" {
		return
	}
	sub.Added = time.Now().UTC().Format(time.RFC3339)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subs[sub.Endpoint] = sub
	p.saveLocked()
}

func (p *pusher) remove(endpoint string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.subs, endpoint)
	p.saveLocked()
}

func (p *pusher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.subs)
}

// pushPayload is what the service worker receives.
type pushPayload struct {
	Title    string `json:"title"`
	Body     string `json:"body"`
	Kind     string `json:"kind"`
	ThreadID string `json:"threadId,omitempty"`
	ID       string `json:"id"`
}

// send delivers one notification to every subscribed browser. A subscription
// the push service rejects as gone is dropped — an uninstalled or expired
// registration would otherwise fail forever.
func (p *pusher) send(payload pushPayload, log func(string, error)) {
	p.mu.Lock()
	targets := make([]subscription, 0, len(p.subs))
	for _, sub := range p.subs {
		targets = append(targets, sub)
	}
	p.mu.Unlock()
	if len(targets) == 0 {
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return
	}

	for _, sub := range targets {
		s := &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.Keys.P256dh, Auth: sub.Keys.Auth},
		}
		resp, err := webpush.SendNotification(body, s, &webpush.Options{
			VAPIDPublicKey:  p.keys.Public,
			VAPIDPrivateKey: p.keys.Private,
			// A contact for the push service; mailto is the convention and no
			// mail is ever sent to it.
			Subscriber: "mailto:shell3@localhost",
			TTL:        60 * 60 * 24,
			Urgency:    webpush.UrgencyNormal,
		})
		if err != nil {
			log("webui: push failed", err)
			continue
		}
		status := resp.StatusCode
		resp.Body.Close()

		// 404/410 mean the browser threw the subscription away.
		if status == http.StatusNotFound || status == http.StatusGone {
			p.remove(sub.Endpoint)
		}
	}
}

// ------------------------------------------------------------------ handlers

// handlePushKey hands the browser the public VAPID key it needs to subscribe,
// and reports whether push is available at all.
func (s *Server) handlePushKey(w http.ResponseWriter, _ *http.Request) {
	if s.push == nil {
		writeJSON(w, map[string]any{"available": false})
		return
	}
	writeJSON(w, map[string]any{
		"available":     true,
		"publicKey":     s.push.keys.Public,
		"subscriptions": s.push.count(),
	})
}

// handlePushSubscribe records a browser's subscription (POST) or forgets it
// (DELETE), so notifications reach it while the tab is closed.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if s.push == nil {
		http.Error(w, "push is unavailable", http.StatusNotImplemented)
		return
	}

	switch r.Method {
	case http.MethodPost:
		var sub subscription
		if err := json.NewDecoder(r.Body).Decode(&sub); err != nil || sub.Endpoint == "" {
			http.Error(w, "bad subscription", http.StatusBadRequest)
			return
		}
		s.push.add(sub)
		writeJSON(w, map[string]bool{"subscribed": true})

	case http.MethodDelete:
		var body struct {
			Endpoint string `json:"endpoint"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		s.push.remove(body.Endpoint)
		writeJSON(w, map[string]bool{"unsubscribed": true})

	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handlePushTest sends a notification to every subscribed browser, so a user
// can confirm the whole path works before relying on it.
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.push == nil || s.push.count() == 0 {
		http.Error(w, "nothing is subscribed", http.StatusConflict)
		return
	}
	s.pushNotification(notification{
		Kind: "note", Title: "shell3", Body: "Push notifications are working.",
	})
	writeJSON(w, map[string]bool{"sent": true})
}

// pushNotification mirrors a bell notification out to subscribed browsers.
func (s *Server) pushNotification(n notification) {
	if s.push == nil {
		return
	}
	title := n.Title
	if title == "" {
		title = "shell3"
	}
	go s.push.send(pushPayload{
		Title:    title,
		Body:     strutil.Truncate(strings.TrimSpace(n.Body), 300),
		Kind:     n.Kind,
		ThreadID: n.ThreadID,
		ID:       n.ID,
	}, func(msg string, err error) {
		// A single failing endpoint is normal (a phone offline, a stale
		// registration); it is a log line, not an event.
		if err != nil && !errors.Is(err, http.ErrHandlerTimeout) {
			s.log.Warn(msg, "error", err.Error())
		}
	})
}
