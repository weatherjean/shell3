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

	provider := prompt(scanner, w, "Provider (ollama/openai/z_ai/codex_plus): ")
	baseURL := prompt(scanner, w, "Base URL (leave empty for provider default): ")
	apiKey := prompt(scanner, w, "API Key (leave empty if not required): ")

	if err := WriteCredentials(homeDir, provider, apiKey, baseURL); err != nil {
		return err
	}
	fmt.Fprintf(w, "Credentials for %q saved.\n", provider)
	return nil
}

func prompt(s *bufio.Scanner, w io.Writer, question string) string {
	fmt.Fprint(w, question)
	if s.Scan() {
		return strings.TrimSpace(s.Text())
	}
	return ""
}
