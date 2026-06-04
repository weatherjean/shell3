package luacfg

import (
	"strings"
	"testing"
)

func TestDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "# comment\nFOO=bar\nQUOTED=\"a b\"\n\nEMPTY=\n")
	got, err := loadDotEnv(dir + "/.env")
	if err != nil {
		t.Fatal(err)
	}
	if got["FOO"] != "bar" || got["QUOTED"] != "a b" || got["EMPTY"] != "" {
		t.Fatalf("dotenv parse: %+v", got)
	}
}

func TestDotEnvForms(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", strings.Join([]string{
		"export EXPORTED=val",
		"SINGLE='sval'",
		"INLINE=plain # trailing comment",
		"HASHED=\"a#b\"",
		"SINGLE_HASH='c#d'",
		"QUOTED_COMMENT=\"q v\" # note",
		"SPACED=\"a b\"",
		"EMPTY=",
		"NAKED_HASH=foo#bar",
	}, "\n")+"\n")
	got, err := loadDotEnv(dir + "/.env")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"EXPORTED":       "val",
		"SINGLE":         "sval",
		"INLINE":         "plain",
		"HASHED":         "a#b",
		"SINGLE_HASH":    "c#d",
		"QUOTED_COMMENT": "q v",
		"SPACED":         "a b",
		"EMPTY":          "",
		"NAKED_HASH":     "foo#bar",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q want %q", k, got[k], v)
		}
	}
	if _, ok := got["export EXPORTED"]; ok {
		t.Errorf("export prefix not trimmed from key: %+v", got)
	}
}

func TestDotEnvMissingFile(t *testing.T) {
	got, err := loadDotEnv(t.TempDir() + "/nope.env")
	if err != nil {
		t.Fatalf("missing .env should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing .env should yield empty map, got %+v", got)
	}
}
