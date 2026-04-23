package config

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// RunAuthInteractive prompts the user for provider credentials and writes them.
func RunAuthInteractive(homeDir string, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)

	fmt.Fprintln(w, "Configure an OpenAI-compatible LLM provider.")
	fmt.Fprintln(w, "Works with any provider: Ollama, OpenAI, Anthropic (via proxy), Together, etc.")
	fmt.Fprintln(w)

	provider := prompt(scanner, w, "Provider name (used as a key, e.g. ollama, openai, my-provider): ")
	baseURL := prompt(scanner, w, "Base URL (e.g. http://localhost:11434/v1 or https://api.openai.com/v1): ")
	apiKey := prompt(scanner, w, "API key (leave empty if not required): ")
	model := prompt(scanner, w, "Default model (e.g. kimi-k2.6:cloud, llama3.2, gpt-4o): ")

	if err := WriteCredentials(homeDir, provider, apiKey, baseURL, model); err != nil {
		return err
	}
	fmt.Fprintf(w, "\nCredentials for %q saved.\n", provider)
	return nil
}

func prompt(s *bufio.Scanner, w io.Writer, question string) string {
	fmt.Fprint(w, question)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}
