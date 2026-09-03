//go:build unix

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/inbox"
)

func TestNotifyCommandEndToEnd(t *testing.T) {
	root := t.TempDir()
	cmd := newNotifyCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--state", root, "--to", "wrk:run-1", "--event", "build.finished", "done"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var receipt inbox.Receipt
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if !receipt.Persisted || receipt.Wake != "unavailable" {
		t.Fatalf("receipt = %+v", receipt)
	}
	var found bool
	err := filepath.Walk(filepath.Join(root, "inbox"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && filepath.Base(path) == receipt.ID+".json" {
			found = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("persisted inbox message not found")
	}
}
