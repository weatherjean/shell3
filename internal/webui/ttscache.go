//go:build unix

package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/weatherjean/shell3/internal/media"
)

// Spoken replies are cached on disk. Pressing read-aloud twice on the same
// message used to pay for the model call twice; now the second press plays the
// file the first one produced.
//
// The key covers everything that changes the audio — the text, the model, the
// voice, and the format — so editing shell3.yaml to a different voice produces
// new files rather than serving the old voice forever.

// ttsCachePrefix marks cached speech in the media directory, so it is
// recognisable beside uploads (`up-*`) and generated images (`img-*`).
const ttsCachePrefix = "tts-"

// ttsCache serves spoken text from disk, generating it once.
type ttsCache struct {
	// generate is the media client's Speak. It writes a fresh file per call.
	generate func(text string) (media.Speech, error)
	// fingerprint distinguishes one voice configuration from another.
	fingerprint string

	// mu serializes generation so two simultaneous plays of the same text make
	// one model call rather than two.
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func newTTSCache(generate func(string) (media.Speech, error), fingerprint string) *ttsCache {
	return &ttsCache{
		generate:    generate,
		fingerprint: fingerprint,
		locks:       map[string]*sync.Mutex{},
	}
}

// key hashes the text with the voice configuration.
func (c *ttsCache) key(text string) string {
	sum := sha256.Sum256([]byte(c.fingerprint + "\x00" + text))
	return hex.EncodeToString(sum[:])[:20]
}

// lockFor returns the per-key mutex, creating it on first use.
func (c *ttsCache) lockFor(key string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.locks[key]; ok {
		return l
	}
	l := &sync.Mutex{}
	c.locks[key] = l
	return l
}

// Speak returns the path to audio for text, generating and storing it on a
// miss. The returned bool reports whether the file was already there, which
// the handler passes back so the interface can say what happened.
func (c *ttsCache) Speak(text string) (path string, cached bool, err error) {
	dir, err := media.Dir()
	if err != nil {
		return "", false, err
	}
	key := c.key(text)

	// One generation at a time per key: two plays of the same reply that race
	// would otherwise both call the model.
	lock := c.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	if hit := findCached(dir, key); hit != "" {
		return hit, true, nil
	}

	speech, err := c.generate(text)
	if err != nil {
		return "", false, err
	}

	// Keep the generated extension: the format is a config choice and the
	// browser needs the right content type.
	stored := filepath.Join(dir, ttsCachePrefix+key+filepath.Ext(speech.Path))
	if err := move(speech.Path, stored); err != nil {
		// Storing failed, but the audio exists — play it and let it be
		// collected as a temporary file rather than failing the request.
		return speech.Path, false, nil
	}
	return stored, false, nil
}

// findCached looks for any stored audio under this key, whatever its
// extension.
func findCached(dir, key string) string {
	matches, err := filepath.Glob(filepath.Join(dir, ttsCachePrefix+key+".*"))
	if err != nil || len(matches) == 0 {
		return ""
	}
	if info, err := os.Stat(matches[0]); err != nil || info.Size() == 0 {
		return ""
	}
	return matches[0]
}

// move renames src to dst, falling back to a copy when they are on different
// filesystems (the media dir and the temp dir often are).
func move(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	os.Remove(src)
	return nil
}
