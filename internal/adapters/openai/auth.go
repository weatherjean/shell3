package openai

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
)

// Auth prompts for an OpenAI-compatible instance's connection settings and
// persists them.
func (p *provider) Auth(_ context.Context, w io.Writer, store *config.CredStore, instance string) error {
	if instance == "" {
		return fmt.Errorf("openai: instance name required")
	}
	in := p.stdin
	if in == nil {
		in = os.Stdin
	}
	scanner := bufio.NewScanner(in)

	_, _ = fmt.Fprintln(w, "Configure an OpenAI-compatible LLM provider.")
	_, _ = fmt.Fprintln(w, "Works with Ollama, OpenAI, Anthropic (via proxy), Together, OpenRouter, etc.")
	_, _ = fmt.Fprintln(w)

	baseURL := promptLine(scanner, w, "Base URL (e.g. http://localhost:11434/v1 or https://api.openai.com/v1): ")
	apiKey := promptLine(scanner, w, "API key (leave empty if not required): ")
	model := promptLine(scanner, w, "Default model (comma-separate for multiple, e.g. gpt-4o,gpt-4o-mini): ")

	if err := store.Set(instance, "openai", map[string]string{
		"base_url":      baseURL,
		"api_key":       apiKey,
		"default_model": model,
	}); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(w, "\nInstance %q saved.\n", instance)
	return nil
}

func promptLine(s *bufio.Scanner, w io.Writer, q string) string {
	_, _ = fmt.Fprint(w, q)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}
