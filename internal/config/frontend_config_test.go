package config

import "testing"

// The telegram token is a secret like every other: it lives in .env and
// reaches the YAML as an env: reference.
func TestParseYAMLTelegram(t *testing.T) {
	c, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\ntelegram:\n  token: env:TELEGRAM_TOKEN\n  chat_id: \"8701499393\"\n",
		map[string]string{"TELEGRAM_TOKEN": "123:abc"})
	if err != nil {
		t.Fatal(err)
	}
	tg := c.Telegram()
	if tg.Token != "123:abc" {
		t.Errorf("Telegram().Token = %q, want the resolved secret", tg.Token)
	}
	if tg.ChatID != "8701499393" {
		t.Errorf("Telegram().ChatID = %q, want 8701499393", tg.ChatID)
	}
}

func TestParseYAMLTelegramUnknownKey(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\ntelegram:\n  dashboard: {}\n"
	if _, err := parseY(t, y, nil); err == nil {
		t.Fatal("expected unknown-key load error for telegram.dashboard")
	}
}

// Present tells "no telegram: block" apart from "block with blank fields" —
// the second is what `shell3 boot` writes when the user defers the token, and
// what `shell3 health` must fail on.
func TestParseYAMLTelegramPresent(t *testing.T) {
	blank, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\ntelegram:\n  token: \"\"\n  chat_id: \"\"\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tg := blank.Telegram(); !tg.Present {
		t.Error("a declared telegram: block must report Present even with blank fields")
	}
	none, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if tg := none.Telegram(); tg.Present {
		t.Error("an absent telegram: block must not report Present")
	}
}
