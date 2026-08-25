package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextFile is one resolved context: entry. Path is the config-dir-relative
// path used for its `### <path>` heading, so the agent knows where to
// edit_file its own brain; Body is the contents, or a stub when the file has
// disappeared since load.
type ContextFile struct {
	Path string
	Body string
	// Size is the true size on disk, above len(Body) when the cap elided it.
	Size int64
}

// A context: file is re-read into the prompt at EVERY turn, so its size is a
// tax paid many times per session — and one the agent silently raises itself,
// being handed edit_file and invited to treat the file as its brain.
// Unbounded, the loop ends badly: once the file alone pushes the prompt past
// compact_at, compaction fires every turn, discards history, and reclaims
// nothing, because the prompt is re-rendered fresh. The session deadlocks
// rather than degrading. Observed live 2026-08-18: a memory.md at 90 KB,
// growing 17k tokens/day, with no warning at any layer.
const (
	// WarnContextBytes is where the operator is told. Diagnostics only.
	WarnContextBytes = 32 << 10
	// MaxContextBytes is the hard ceiling on what reaches the prompt.
	MaxContextBytes = 64 << 10
	// contextHeadFrac is the head's share of the surviving budget. Head AND
	// tail, not tail alone: a brain file reliably drifts into a curated
	// header plus an append-only tail, and a tail-only window would drop the
	// standing instructions the header exists to carry.
	contextHeadFrac = 3
)

// isContextGlob mirrors the metacharacters filepath.Match understands.
func isContextGlob(entry string) bool {
	return strings.ContainsAny(entry, "*?[")
}

// ResolveContextFiles reads the context: entries against configDir in list
// order, a glob's matches sorted within its entry. A literal entry that
// vanished since load yields a stub rather than an error: a missing brain file
// must never fail a turn. Only a malformed glob errors, and load rejects those.
func ResolveContextFiles(configDir string, entries []string) ([]ContextFile, error) {
	var out []ContextFile
	for _, e := range entries {
		if isContextGlob(e) {
			matches, err := filepath.Glob(filepath.Join(configDir, e))
			if err != nil {
				return nil, fmt.Errorf("context glob %q: %w", e, err)
			}
			sort.Strings(matches)
			for _, m := range matches {
				rel := relForPrompt(configDir, m)
				out = append(out, readContextFile(m, rel))
			}
			continue
		}
		out = append(out, readContextFile(filepath.Join(configDir, e), e))
	}
	return out, nil
}

// readContextFile reads abs, tagged with the relative rel. An unreadable file
// yields the missing-file stub, and a body over MaxContextBytes is elided in
// the middle, so one runaway brain file cannot crowd out the conversation it
// is supposed to inform.
func readContextFile(abs, rel string) ContextFile {
	body, err := os.ReadFile(abs)
	if err != nil {
		return ContextFile{Path: rel, Body: "(context file missing: " + rel + ")"}
	}
	return ContextFile{Path: rel, Body: elideMiddle(string(body), rel), Size: int64(len(body))}
}

// elideMiddle keeps body's head and tail and marks the gap. The marker is
// load-bearing: without it the agent reads a windowed file as whole and acts
// on half its own instructions. It names the file and how to read the rest, so
// the window is a detour rather than a dead end.
func elideMiddle(body, rel string) string {
	if len(body) <= MaxContextBytes {
		return body
	}
	marker := fmt.Sprintf(
		"\n\n... [%s is %d KB, over the %d KB context cap — the middle is elided here. "+
			"Head and tail are shown in full. Read the whole file with `cat %s`, or search it with `rg`. "+
			"This file is re-read into your prompt every turn: trim it.] ...\n\n",
		rel, len(body)>>10, MaxContextBytes>>10, rel)

	budget := MaxContextBytes - len(marker)
	if budget < 0 {
		return marker
	}
	head := budget / contextHeadFrac
	tail := budget - head
	// Line boundaries, so neither window opens or closes mid-sentence.
	if i := strings.LastIndexByte(body[:head], '\n'); i > 0 {
		head = i
	}
	if i := strings.IndexByte(body[len(body)-tail:], '\n'); i >= 0 {
		tail -= i + 1
	}
	return body[:head] + marker + body[len(body)-tail:]
}

// ContextSizeWarning is one oversized context: file. OverCap separates the two
// cases: a large file is expensive but intact, an over-cap one is losing
// content from the prompt. Branch on the field, never on the message text.
type ContextSizeWarning struct {
	Path    string
	Size    int64
	OverCap bool
}

func (w ContextSizeWarning) String() string {
	if w.OverCap {
		return fmt.Sprintf("context file %q is %d KB — over the %d KB cap, so its middle is elided from the prompt",
			w.Path, w.Size>>10, MaxContextBytes>>10)
	}
	return fmt.Sprintf("context file %q is %d KB — it is re-read into the prompt on every turn (cap is %d KB)",
		w.Path, w.Size>>10, MaxContextBytes>>10)
}

// ContextSizeWarnings reports the entries worth the operator's attention, in
// list order. Split from the read path on purpose: the cap applies every turn,
// but the warning belongs once, where a human is looking.
func ContextSizeWarnings(configDir string, entries []string) []ContextSizeWarning {
	files, err := ResolveContextFiles(configDir, entries)
	if err != nil {
		return nil
	}
	var out []ContextSizeWarning
	for _, f := range files {
		if f.Size < WarnContextBytes {
			continue
		}
		out = append(out, ContextSizeWarning{Path: f.Path, Size: f.Size, OverCap: f.Size > MaxContextBytes})
	}
	return out
}

// relForPrompt renders m relative to configDir, falling back to m if it
// escapes.
func relForPrompt(configDir, m string) string {
	rel, err := filepath.Rel(configDir, m)
	if err != nil {
		return m
	}
	return filepath.ToSlash(rel)
}

// RenderContext renders an agent's context: entries as the prompt's
// `## Context` section, one heading per file. "" when there are none, and ""
// rather than an error on a malformed glob — a brain file must never break
// the agent.
func RenderContext(configDir string, entries []string) string {
	if len(entries) == 0 {
		return ""
	}
	files, err := ResolveContextFiles(configDir, entries)
	if err != nil || len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Context\n")
	for _, f := range files {
		b.WriteString("\n### " + f.Path + "\n\n" + f.Body + "\n")
	}
	return b.String()
}
