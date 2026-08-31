//go:build unix

package main

import "testing"

type recordingAllowFrom struct {
	calls int
	ids   []string
}

func (r *recordingAllowFrom) SetAllowFrom(ids []string) error {
	r.calls++
	r.ids = ids
	return nil
}

func TestConfigureAllowFromSkipsTelegramIDsForConsole(t *testing.T) {
	var got recordingAllowFrom
	if err := configureAllowFrom(&got, true, []string{"42"}); err != nil {
		t.Fatal(err)
	}
	if got.calls != 0 {
		t.Fatal("console replaced its synthetic sender allowlist with Telegram ids")
	}

	if err := configureAllowFrom(&got, false, []string{"42"}); err != nil {
		t.Fatal(err)
	}
	if got.calls != 1 || len(got.ids) != 1 || got.ids[0] != "42" {
		t.Fatalf("Bot API allowlist call = %d, %v; want 1, [42]", got.calls, got.ids)
	}
}
