package chat

import (
	"strings"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/persona"
)

// capturingLog records Warn lines so a test can assert on the diagnostic the
// operator would actually see.
type capturingLog struct {
	mu    sync.Mutex
	warns []string
}

func (l *capturingLog) Debug(string, ...any)        {}
func (l *capturingLog) Info(string, ...any)         {}
func (l *capturingLog) Error(string, error, ...any) {}
func (l *capturingLog) Warn(msg string, fields ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}

func (l *capturingLog) matching(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, w := range l.warns {
		if strings.Contains(w, sub) {
			n++
		}
	}
	return n
}

const overheadWarnSub = "compaction budget"

// A system prompt over half of compact_at is the deadlock precondition: it is
// re-rendered from disk every turn, so compaction can never reclaim it. The
// operator must be told, because the only other symptom is the provider
// rejecting the request for length much later.
func TestWarnFixedOverhead_FiresWhenSystemPromptDominates(t *testing.T) {
	log := &capturingLog{}
	cfg := TurnConfig{
		// ~4 chars/token: 40k chars of prompt against a 1000-token threshold.
		Personality: persona.Persona{SystemPrompt: strings.Repeat("brain ", 8000)},
		AgentKnobs:  AgentKnobs{CompactAt: 1000},
		ToolConfig:  ToolConfig{Log: log},
	}
	sess := &Session{}

	warnFixedOverhead(cfg, sess)
	if got := log.matching(overheadWarnSub); got != 1 {
		t.Fatalf("warned %d times, want 1", got)
	}

	// The condition stays true on every subsequent turn, so an unthrottled
	// warning would bury the log it exists to make readable.
	for i := 0; i < 5; i++ {
		warnFixedOverhead(cfg, sess)
	}
	if got := log.matching(overheadWarnSub); got != 1 {
		t.Errorf("warned %d times across 6 calls, want 1 (throttle broken)", got)
	}
}

// The common case must stay silent: a small system prompt means compaction is
// working on the part of the window that actually holds the tokens.
func TestWarnFixedOverhead_SilentWhenSystemPromptIsSmall(t *testing.T) {
	log := &capturingLog{}
	cfg := TurnConfig{
		Personality: persona.Persona{SystemPrompt: "you are a helpful agent"},
		AgentKnobs:  AgentKnobs{CompactAt: 1000},
		ToolConfig:  ToolConfig{Log: log},
	}
	warnFixedOverhead(cfg, &Session{})
	if got := log.matching(overheadWarnSub); got != 0 {
		t.Errorf("warned %d times on a small system prompt, want 0", got)
	}
}

// RefreshPrompt is the whole reason this warning is needed — the prompt is
// re-read from disk per turn, so the size that matters is the refreshed one,
// not the snapshot taken at session build.
func TestWarnFixedOverhead_MeasuresRefreshedPrompt(t *testing.T) {
	log := &capturingLog{}
	cfg := TurnConfig{
		Personality:   persona.Persona{SystemPrompt: "small at build time"},
		RefreshPrompt: func() string { return strings.Repeat("grown ", 8000) },
		AgentKnobs:    AgentKnobs{CompactAt: 1000},
		ToolConfig:    ToolConfig{Log: log},
	}
	warnFixedOverhead(cfg, &Session{})
	if got := log.matching(overheadWarnSub); got != 1 {
		t.Errorf("warned %d times, want 1 — the refreshed prompt is what reaches the model", got)
	}
}
