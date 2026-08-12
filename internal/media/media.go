//go:build unix

// Package media is now vestigial: every OpenAI-compatible media capability
// (imagegen, describe, tts, stt) has been removed from shell3 core in favor
// of wrapper-script recipes. What remains — the durable media directory
// resolution used by attachment storage and the janitor sweep — is kept here
// only until Task 5 deletes this package outright and relocates Dir()/Sweep.
package media

import (
	"github.com/weatherjean/shell3/internal/mediadir"
)

// Clients is empty now that every capability (imagegen, describe, tts, stt)
// has been removed. Kept only so callers mid-migration still compile; Task 5
// deletes this package and its callers together.
type Clients struct{}

// Dir returns shell3's durable media directory — where attachments are
// stored, so every media file the agent has seen keeps a stable path that
// survives reboots and OS temp cleaning (re-readable with read_media,
// re-sendable to the chat, findable from history). Default <configDir>/media
// (which is ~/.shell3/media for the default config dir, see
// mediadir.SetBaseDir); $SHELL3_MEDIA_DIR overrides (tests point it at a
// TempDir). Created on demand.
func Dir() (string, error) {
	return mediadir.Dir()
}
