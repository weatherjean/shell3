//go:build unix

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostFeedbackRestartRequiresExecutedHandoff(t *testing.T) {
	for _, mode := range []string{"queued", "executed", "wrong-handoff", "exec-failed", "delivery-failed"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "host-actions.json")
			before, err := openHostFeedback(path, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := before.record("restart", `{"ok":true,"restart":"queued_after_active_replies"}`, nil); err != nil {
				t.Fatal(err)
			}
			handoff := before.instance
			if mode != "queued" && mode != "delivery-failed" {
				if err := before.executing(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "wrong-handoff" {
				handoff = "unrelated"
			}
			if mode == "exec-failed" {
				if err := before.record("restart", "", errors.New("exec failed")); err != nil {
					t.Fatal(err)
				}
				handoff = ""
			}
			if mode == "delivery-failed" {
				if err := before.record("restart", `{"ok":false,"restart":"cancelled_reply_delivery_failed"}`, nil); err != nil {
					t.Fatal(err)
				}
			}
			after, err := openHostFeedback(path, handoff)
			if err != nil {
				t.Fatal(err)
			}
			last := after.actions[len(after.actions)-1]
			if (last.Action == "restart" && last.Outcome == "completed") != (mode == "executed") {
				t.Fatalf("false restart conclusion: %+v", last)
			}
			if after.instance == before.instance {
				t.Fatal("reused instance ID")
			}
			if !strings.Contains(after.context(), "does not rerun the service launcher") {
				t.Fatal("missing environment semantics")
			}
		})
	}
}

func TestHostFeedbackRecordsOutcomesAndPersistenceFailures(t *testing.T) {
	f, err := openHostFeedback(filepath.Join(t.TempDir(), "host-actions.json"), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		output  string
		err     error
		outcome string
	}{
		{`{"ok":true,"effective":"future_turns"}`, nil, "succeeded"},
		{`{"ok":false,"reload":"rejected"}`, nil, "rejected"},
		{"", errors.New("invalid configuration"), "failed"},
	} {
		if err := f.record("reload", tc.output, tc.err); err != nil {
			t.Fatal(err)
		}
		if got := f.actions[len(f.actions)-1].Outcome; got != tc.outcome {
			t.Fatalf("outcome=%s", got)
		}
	}
	for range 20 {
		if err := f.record("status", `{"ok":true}`, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.actions) != 12 {
		t.Fatal("unbounded journal")
	}
	if err := os.RemoveAll(filepath.Dir(f.path)); err != nil {
		t.Fatal(err)
	}
	if err := f.record("reload", `{"ok":true}`, nil); err == nil {
		t.Fatal("silently lost feedback")
	}
}

func TestHostFeedbackUnavailableHistoryDoesNotPreventStartup(t *testing.T) {
	for _, contents := range []string{`[{"instance":"old","action":"restart","outcome":"executing"},`, strings.Repeat("x", (64<<10)+1)} {
		path := filepath.Join(t.TempDir(), "host-actions.json")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		f, err := openHostFeedback(path, "old")
		if err == nil || f == nil {
			t.Fatalf("need usable feedback and explicit error: %v", err)
		}
		if len(f.actions) != 1 || f.actions[0].Action != "startup" {
			t.Fatalf("trusted incomplete history: %+v", f.actions)
		}
		if !strings.Contains(f.context(), "receipt_persistence_error") {
			t.Fatal("hidden persistence failure")
		}
		if err := f.record("status", `{"ok":true}`, nil); err == nil {
			t.Fatal("overwrote unreadable history")
		}
		if f.actions[len(f.actions)-1].Outcome != "succeeded" {
			t.Fatal("lost in-memory action result")
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != contents {
			t.Fatal("damaged source history")
		}
	}
}
