//go:build unix

package main

import (
	"os"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

func TestCanonicalDBPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	// The DB is always ~/.shell3/data/shell3.db regardless of --config: the
	// runtime always writes there (resolvePaths builds Global from $HOME), so
	// the read CLIs must resolve the same path no matter what config is named.
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
			name:   "config under .shell3 still resolves to HOME",
			config: "/x/.shell3/shell3.lua",
			want:   paths.NewGlobal(home).DB,
		},
		{
			name:   "nested config still resolves to HOME",
			config: "/x/.shell3/telegram/shell3.lua",
			want:   paths.NewGlobal(home).DB,
		},
		{
			name:   "config outside any .shell3 still resolves to HOME",
			config: "/tmp/foo/shell3.lua",
			want:   paths.NewGlobal(home).DB,
		},
		{
			name:   "bare name still resolves to HOME",
			config: "code",
			want:   paths.NewGlobal(home).DB,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := canonicalDBPath()
			if err != nil {
				t.Fatalf("canonicalDBPath() [%s]: %v", tc.config, err)
			}
			if got != tc.want {
				t.Errorf("canonicalDBPath() [%s] = %q, want %q", tc.config, got, tc.want)
			}
		})
	}
}
