package luacfg

import "testing"

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

func TestDotEnvMissingFile(t *testing.T) {
	got, err := loadDotEnv(t.TempDir() + "/nope.env")
	if err != nil {
		t.Fatalf("missing .env should not error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("missing .env should yield empty map, got %+v", got)
	}
}
