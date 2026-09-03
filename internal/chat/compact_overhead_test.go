package chat

import (
	"strings"
	"sync"
	"testing"
)

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

func TestWarnFixedOverhead_FiresWhenSystemPromptDominates(t *testing.T) {
	log := &capturingLog{}
	cfg := TurnConfig{
		Profile:    AgentProfile{SystemPrompt: strings.Repeat("brain ", 8000)},
		AgentKnobs: AgentKnobs{CompactAt: 1000},
		ToolConfig: ToolConfig{Log: log},
	}
	sess := &Session{}

	warnFixedOverhead(cfg, sess)
	if got := log.matching(overheadWarnSub); got != 1 {
		t.Fatalf("warned %d times, want 1", got)
	}

	for i := 0; i < 5; i++ {
		warnFixedOverhead(cfg, sess)
	}
	if got := log.matching(overheadWarnSub); got != 1 {
		t.Errorf("warned %d times across 6 calls, want 1 (throttle broken)", got)
	}
}

func TestWarnFixedOverhead_SilentWhenSystemPromptIsSmall(t *testing.T) {
	log := &capturingLog{}
	cfg := TurnConfig{
		Profile:    AgentProfile{SystemPrompt: "you are a helpful agent"},
		AgentKnobs: AgentKnobs{CompactAt: 1000},
		ToolConfig: ToolConfig{Log: log},
	}
	warnFixedOverhead(cfg, &Session{})
	if got := log.matching(overheadWarnSub); got != 0 {
		t.Errorf("warned %d times on a small system prompt, want 0", got)
	}
}

func TestWarnFixedOverhead_MeasuresConfiguredPrompt(t *testing.T) {
	log := &capturingLog{}
	cfg := TurnConfig{
		Profile:    AgentProfile{SystemPrompt: strings.Repeat("grown ", 8000)},
		AgentKnobs: AgentKnobs{CompactAt: 1000},
		ToolConfig: ToolConfig{Log: log},
	}
	warnFixedOverhead(cfg, &Session{})
	if got := log.matching(overheadWarnSub); got != 1 {
		t.Errorf("warned %d times, want 1", got)
	}
}
