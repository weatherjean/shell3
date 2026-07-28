//go:build unix

package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/media"
)

// Voice endpoints, backed by the `media.stt` / `media.tts` models.

// maxAudioBytes caps an inbound recording. Generous for dictation, small
// enough that a stuck recorder cannot fill the disk.
const maxAudioBytes = 25 << 20

// handleSTT transcribes an uploaded recording. The audio is written to the
// durable media dir (so a transcript can be traced back to what was said) and
// handed to the configured STT model.
func (s *Server) handleSTT(w http.ResponseWriter, r *http.Request) {
	if s.media == nil || s.media.Transcribe == nil {
		http.Error(w, "no speech-to-text model configured", http.StatusNotImplemented)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseMultipartForm(maxAudioBytes); err != nil {
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		http.Error(w, "missing audio", http.StatusBadRequest)
		return
	}
	defer file.Close()

	dir, err := media.Dir()
	if err != nil {
		http.Error(w, "no media directory", http.StatusInternalServerError)
		return
	}
	ext := filepath.Ext(header.Filename)
	if ext == "" {
		ext = ".webm"
	}
	path := filepath.Join(dir, fmt.Sprintf("web-%d%s", time.Now().UnixNano(), ext))

	dst, err := os.Create(path)
	if err != nil {
		http.Error(w, "cannot store audio", http.StatusInternalServerError)
		return
	}
	_, copyErr := io.Copy(dst, io.LimitReader(file, maxAudioBytes))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		http.Error(w, "cannot store audio", http.StatusInternalServerError)
		return
	}

	text, err := s.media.Transcribe(r.Context(), path)
	if err != nil {
		s.log.Error("webui: transcription failed", err)
		http.Error(w, "transcription failed", http.StatusBadGateway)
		return
	}
	writeJSON(w, map[string]string{"text": text})
}

// handleTTS speaks text and returns the audio, from the cache when this exact
// text has been spoken before with this voice. Playing a reply a second time
// should not pay for the model a second time.
func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cache := s.tts
	s.mu.Unlock()

	if cache == nil {
		http.Error(w, "no text-to-speech model configured", http.StatusNotImplemented)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	path, cached, err := cache.Speak(body.Text)
	if err != nil {
		s.log.Error("webui: speech synthesis failed", err)
		http.Error(w, "speech synthesis failed", http.StatusBadGateway)
		return
	}

	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "cannot read audio", http.StatusInternalServerError)
		return
	}
	defer f.Close()

	w.Header().Set("Content-Type", contentTypeFor(path))
	// Keyed by a hash of the text and voice, so a given URL's bytes never
	// change; the browser may keep it too.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	if cached {
		w.Header().Set("X-Shell3-Cache", "hit")
	} else {
		w.Header().Set("X-Shell3-Cache", "miss")
	}
	_, _ = io.Copy(w, f)
}

func contentTypeFor(path string) string {
	switch filepath.Ext(path) {
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".m4a", ".mp4":
		return "audio/mp4"
	default:
		return "audio/mpeg"
	}
}
