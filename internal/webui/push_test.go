//go:build unix

package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/media"
)

// The VAPID keypair identifies this install to push services. It is generated
// once and reused: regenerating it would silently invalidate every existing
// subscription.
func TestPushKeysAreStableAcrossRestarts(t *testing.T) {
	dir := t.TempDir()

	first, err := newPusher(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newPusher(dir)
	if err != nil {
		t.Fatal(err)
	}

	if first.keys.Public != second.keys.Public || first.keys.Private != second.keys.Private {
		t.Error("the keypair must survive a restart, or every subscription breaks")
	}
	if first.keys.Public == "" || first.keys.Private == "" {
		t.Fatal("a keypair should have been generated")
	}
}

// The private key signs pushes for this install; it must not be world-readable.
func TestPushKeysAreOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	if _, err := newPusher(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, pushKeysFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}
}

func TestSubscriptionsSurviveARestart(t *testing.T) {
	dir := t.TempDir()

	p, err := newPusher(dir)
	if err != nil {
		t.Fatal(err)
	}
	var sub subscription
	sub.Endpoint = "https://push.example/abc"
	sub.Keys.P256dh = "key"
	sub.Keys.Auth = "auth"
	p.add(sub)

	reopened, err := newPusher(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.count() != 1 {
		t.Errorf("subscriptions = %d after restart, want 1", reopened.count())
	}

	reopened.remove(sub.Endpoint)
	again, err := newPusher(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.count() != 0 {
		t.Errorf("a removed subscription came back: %d", again.count())
	}
}

// Re-subscribing the same browser must not accumulate duplicates, or one
// notification arrives several times.
func TestResubscribingIsIdempotent(t *testing.T) {
	p, err := newPusher(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var sub subscription
	sub.Endpoint = "https://push.example/abc"
	for range 3 {
		p.add(sub)
	}
	if p.count() != 1 {
		t.Errorf("subscriptions = %d, want 1", p.count())
	}
}

func TestSubscriptionWithoutAnEndpointIsRejected(t *testing.T) {
	p, err := newPusher(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p.add(subscription{})
	if p.count() != 0 {
		t.Error("a subscription with no endpoint is not usable and must be dropped")
	}
}

func TestPushKeyEndpointReportsAvailability(t *testing.T) {
	srv := newTestServer(t, "ok")

	rec := httptest.NewRecorder()
	srv.handlePushKey(rec, httptest.NewRequest(http.MethodGet, "/api/push", nil))

	var got struct {
		Available bool   `json:"available"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || got.PublicKey == "" {
		t.Errorf("push should be available with a public key: %+v", got)
	}
	// The private half must never leave the server.
	if strings.Contains(rec.Body.String(), srv.push.keys.Private) {
		t.Fatal("the private VAPID key leaked to the browser")
	}
}

func TestPushSubscribeRoundTrip(t *testing.T) {
	srv := newTestServer(t, "ok")

	body := `{"endpoint":"https://push.example/x","keys":{"p256dh":"k","auth":"a"}}`
	rec := httptest.NewRecorder()
	srv.handlePushSubscribe(rec,
		httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(body)))
	if rec.Code != http.StatusOK || srv.push.count() != 1 {
		t.Fatalf("subscribe failed: status %d, count %d", rec.Code, srv.push.count())
	}

	rec = httptest.NewRecorder()
	srv.handlePushSubscribe(rec, httptest.NewRequest(http.MethodDelete,
		"/api/push/subscribe", strings.NewReader(`{"endpoint":"https://push.example/x"}`)))
	if rec.Code != http.StatusOK || srv.push.count() != 0 {
		t.Fatalf("unsubscribe failed: status %d, count %d", rec.Code, srv.push.count())
	}
}

func TestPushSubscribeRejectsGarbage(t *testing.T) {
	srv := newTestServer(t, "ok")
	rec := httptest.NewRecorder()
	srv.handlePushSubscribe(rec, httptest.NewRequest(http.MethodPost,
		"/api/push/subscribe", strings.NewReader(`{"endpoint":""}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// Testing push with nothing subscribed should say so rather than pretend.
func TestPushTestWithoutSubscribersConflicts(t *testing.T) {
	srv := newTestServer(t, "ok")
	rec := httptest.NewRecorder()
	srv.handlePushTest(rec, httptest.NewRequest(http.MethodPost, "/api/push/test", nil))
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

// Push is a bonus channel: with no pusher the server still runs and the bell
// still works.
func TestNotificationsWorkWithoutPush(t *testing.T) {
	srv := newTestServer(t, "ok")
	srv.push = nil

	events, cancel := srv.hub.subscribe()
	defer cancel()

	srv.PostCompletion("", "", "still delivered")

	select {
	case ev := <-events:
		if ev.Name != "notification" {
			t.Errorf("event = %q, want notification", ev.Name)
		}
	default:
		t.Fatal("the bell should still receive notifications without push")
	}
}

// Speaking the same text twice must not pay for the model twice — the whole
// point of the cache.
func TestTTSCacheReusesTheGeneratedAudio(t *testing.T) {
	t.Setenv("SHELL3_MEDIA_DIR", t.TempDir())

	calls := 0
	cache := newTTSCache(func(text string) (media.Speech, error) {
		calls++
		path := filepath.Join(t.TempDir(), "spoken.mp3")
		if err := os.WriteFile(path, []byte("audio for "+text), 0o644); err != nil {
			return media.Speech{}, err
		}
		return media.Speech{Path: path}, nil
	}, "model|voice|mp3")

	first, cached, err := cache.Speak("hello there")
	if err != nil {
		t.Fatal(err)
	}
	if cached {
		t.Error("the first call cannot be a cache hit")
	}

	second, cached, err := cache.Speak("hello there")
	if err != nil {
		t.Fatal(err)
	}
	if !cached || second != first {
		t.Errorf("second call: cached=%v path=%q, want the first file", cached, second)
	}
	if calls != 1 {
		t.Errorf("the model was called %d times, want 1", calls)
	}

	// Different text is a different file.
	if _, cached, _ := cache.Speak("something else"); cached {
		t.Error("different text must not hit the cache")
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
}

// Changing the voice in the config must produce new audio, not replay the old
// voice forever.
func TestTTSCacheKeyIncludesTheVoice(t *testing.T) {
	a := newTTSCache(nil, "model|alice|mp3")
	b := newTTSCache(nil, "model|bob|mp3")
	if a.key("same words") == b.key("same words") {
		t.Error("two voices must not share a cache key")
	}
}

// The generated file lands in the media dir under a recognisable name, so it
// sits beside uploads and generated images rather than in a temp dir.
func TestTTSCacheStoresInTheMediaDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SHELL3_MEDIA_DIR", dir)

	cache := newTTSCache(func(string) (media.Speech, error) {
		path := filepath.Join(t.TempDir(), "spoken.mp3")
		return media.Speech{Path: path}, os.WriteFile(path, []byte("audio"), 0o644)
	}, "fingerprint")

	path, _, err := cache.Speak("store me")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Errorf("stored at %q, want it in the media dir %q", path, dir)
	}
	if !strings.HasPrefix(filepath.Base(path), ttsCachePrefix) {
		t.Errorf("name %q should start with %q", filepath.Base(path), ttsCachePrefix)
	}
}
