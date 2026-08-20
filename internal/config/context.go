package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextFile is one resolved `context:` entry: Path is the config-dir-relative
// path used for its `### <path>` prompt heading (so the agent knows where to
// edit_file its own brain), Body is the file's contents (or a one-line stub
// when a literal file has disappeared since load).
type ContextFile struct {
	Path string
	Body string
	// Size is the file's true size on disk, which exceeds len(Body) when the
	// body was elided by the cap below.
	Size int64
}

// A `context:` file is re-read into the system prompt at EVERY turn of every
// run, so its size is a tax paid many times per session — and, because the
// agent is handed edit_file and invited to treat the file as its brain, it is
// a tax the agent itself silently raises. Left unbounded the loop ends badly:
// once the file alone pushes the prompt past compact_at, compaction fires
// every turn, discards conversation history, and reclaims nothing (the system
// prompt is re-rendered fresh each turn) — the session deadlocks rather than
// degrading. Observed live 2026-08-18: a memory.md grew to 90 KB at
// +17k tokens/day with no warning at any layer.
const (
	// WarnContextBytes is where the operator gets told (load warning, and a
	// shell3 health failure). Diagnostics only — nothing is elided here.
	WarnContextBytes = 32 << 10
	// MaxContextBytes is the hard ceiling on what reaches the prompt.
	MaxContextBytes = 64 << 10
	// contextHeadFrac is how much of the surviving budget goes to the head.
	// Head AND tail are kept rather than tail alone: an agent brain file
	// reliably drifts into a curated header (standing traps, where things
	// are) plus an append-only tail, and a tail-only window would silently
	// drop exactly the standing instructions the header exists to carry.
	contextHeadFrac = 3
)

// isContextGlob reports whether entry is a glob pattern (vs a literal path).
// Mirrors the metacharacters filepath.Match understands.
func isContextGlob(entry string) bool {
	return strings.ContainsAny(entry, "*?[")
}

// ResolveContextFiles reads the main agent's `context:` entries against
// configDir, in list order (a glob's matches sorted lexically within its
// entry). A literal entry that has vanished since config load yields a stub
// body rather than an error — a missing brain file must never fail a turn.
// The only error path is a malformed glob pattern (already rejected at load).
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

// readContextFile reads abs, tagging the result with the config-dir-relative
// rel; an unreadable file (e.g. deleted between load and session build) yields
// the missing-file stub instead of an error. A body over MaxContextBytes is
// elided in the middle (see elideMiddle) so one runaway brain file can never
// crowd out the conversation it is supposed to inform.
func readContextFile(abs, rel string) ContextFile {
	body, err := os.ReadFile(abs)
	if err != nil {
		return ContextFile{Path: rel, Body: "(context file missing: " + rel + ")"}
	}
	return ContextFile{Path: rel, Body: elideMiddle(string(body), rel), Size: int64(len(body))}
}

// elideMiddle caps body at MaxContextBytes by keeping its head and tail and
// replacing the middle with a marker. The marker is load-bearing: without it
// the agent reads a windowed file as if it were whole, and silently acts on
// half its own instructions. It names the file and the tool to read the rest,
// so a windowed view is a detour rather than a dead end.
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
	// Prefer line boundaries so neither window opens or closes mid-sentence.
	if i := strings.LastIndexByte(body[:head], '\n'); i > 0 {
		head = i
	}
	if i := strings.IndexByte(body[len(body)-tail:], '\n'); i >= 0 {
		tail -= i + 1
	}
	return body[:head] + marker + body[len(body)-tail:]
}

// ContextSizeWarning is one oversized `context:` file. OverCap distinguishes
// the two cases callers must treat differently: a large file is expensive but
// intact (advice), while an over-cap file is losing content from the prompt
// (a defect). Callers branch on the field, never on the message text.
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

// ContextSizeWarnings reports the resolved `context:` entries large enough to
// be worth the operator's attention, in list order. Split from the read path
// deliberately: the cap must apply on every turn, but the warning should be
// emitted once, where a human is actually looking.
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

// relForPrompt renders m relative to configDir with forward slashes, falling
// back to m if it somehow escapes configDir.
func relForPrompt(configDir, m string) string {
	rel, err := filepath.Rel(configDir, m)
	if err != nil {
		return m
	}
	return filepath.ToSlash(rel)
}

// RenderContext reads an agent's context: entries and renders them as the
// prompt's `## Context` section, one `### <path>` per file. Returns "" when
// there are no entries. A malformed glob (rejected at load) yields "" rather
// than failing a turn — a brain file must never break the agent.
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
