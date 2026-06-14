//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

func TestCanonicalDBPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "no config uses HOME default",
			config: "",
			want:   paths.NewGlobal(home).DB,
		},
		{
			name:   "config under .shell3 anchors to its data dir",
			config: "/x/.shell3/shell3.lua",
			want:   "/x/.shell3/data/shell3.db",
		},
		{
			name:   "config in nested subdir anchors to nearest .shell3",
			config: "/x/.shell3/telegram/shell3.lua",
			want:   "/x/.shell3/data/shell3.db",
		},
		{
			name:   "config with no .shell3 ancestor falls back to its dir",
			config: "/tmp/foo/shell3.lua",
			want:   filepath.Join("/tmp/foo", "data", "shell3.db"),
		},
		{
			name:   "bare name expands under ~/.shell3 and anchors there",
			config: "code",
			want:   paths.NewGlobal(home).DB,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalDBPath(tc.config)
			if err != nil {
				t.Fatalf("canonicalDBPath(%q): %v", tc.config, err)
			}
			if got != tc.want {
				t.Errorf("canonicalDBPath(%q) = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}
