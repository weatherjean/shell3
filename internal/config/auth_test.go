package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestWriteCredentials(t *testing.T) {
	dir := t.TempDir()
	err := config.WriteCredentials(dir, "ollama", "", "http://localhost:11434/v1")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Error("expected non-empty credentials file")
	}
}
