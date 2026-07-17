package luacfg

import (
	"path/filepath"
	"testing"
)

// mediaConfig wraps a media-block declaration in a minimal valid config (one
// model, one agent), mirroring hbConfig in heartbeat_test.go.
func mediaConfig(block string) string {
	return `
shell3.model("m", { base_url = "http://x", model = "id" })
shell3.agent({ name="code", model="m", prompt="hi", tools={} })
` + block
}

func TestMediaBlocksParse(t *testing.T) {
	c := mustLoad(t, mediaConfig(`
shell3.stt{ model = "m", language = "en" }
shell3.tts{ model = "m", voice = "alloy" }
shell3.describe{ model = "m" }
shell3.imagegen{ model = "m" }
`))
	if s := c.STT(); s == nil || s.ModelRef != "m" || s.Language != "en" || !s.Echo {
		t.Fatalf("stt = %+v", c.STT())
	}
	if ts := c.TTS(); ts == nil || ts.Voice != "alloy" || ts.Mode != "inbound" || ts.Format != "opus" {
		t.Fatalf("tts = %+v", c.TTS())
	}
	if d := c.Describe(); d == nil || d.Prompt != "Describe the image." {
		t.Fatalf("describe = %+v", c.Describe())
	}
	if ig := c.Imagegen(); ig == nil || ig.Size != "1024x1024" {
		t.Fatalf("imagegen = %+v", c.Imagegen())
	}
}

func TestMediaBlockAbsent(t *testing.T) {
	c := mustLoad(t, mediaConfig(""))
	if c.STT() != nil {
		t.Fatalf("want nil STT, got %+v", c.STT())
	}
	if c.TTS() != nil {
		t.Fatalf("want nil TTS, got %+v", c.TTS())
	}
	if c.Describe() != nil {
		t.Fatalf("want nil Describe, got %+v", c.Describe())
	}
	if c.Imagegen() != nil {
		t.Fatalf("want nil Imagegen, got %+v", c.Imagegen())
	}
}

func TestMediaUnknownModelRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", mediaConfig(`
shell3.stt{ model = "nope" }
`))
	_, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !contains(err.Error(), "stt") || !contains(err.Error(), "nope") {
		t.Fatalf("error should mention stt and nope, got: %v", err)
	}
}

func TestMediaModelDeclaredAfterBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.stt{ model = "m", language = "en" }
shell3.model("m", { base_url = "http://x", model = "id" })
shell3.agent({ name="code", model="m", prompt="hi", tools={} })
`)
	c, err := Load(filepath.Join(dir, "shell3.lua"))
	if err != nil {
		t.Fatalf("want no error (model-ref validation is post-parse), got: %v", err)
	}
	defer c.Close()
	if s := c.STT(); s == nil || s.ModelRef != "m" {
		t.Fatalf("stt = %+v", c.STT())
	}
}

func TestMediaDuplicateBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", mediaConfig(`
shell3.tts{ model = "m" }
shell3.tts{ model = "m" }
`))
	_, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !contains(err.Error(), "only one") {
		t.Fatalf("error should mention 'only one', got: %v", err)
	}
}

func TestMediaBadMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", mediaConfig(`
shell3.tts{ model = "m", mode = "loud" }
`))
	_, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, want := range []string{"off", "inbound", "always"} {
		if !contains(err.Error(), want) {
			t.Fatalf("error should list off|inbound|always, got: %v", err)
		}
	}
}

func TestImagegenAPIDefaultOpenAI(t *testing.T) {
	c := mustLoad(t, mediaConfig(`
shell3.imagegen{ model = "m" }
`))
	if ig := c.Imagegen(); ig == nil || ig.API != "openai" {
		t.Fatalf("imagegen = %+v, want API=openai", c.Imagegen())
	}
}

func TestImagegenAPIOpenRouter(t *testing.T) {
	c := mustLoad(t, mediaConfig(`
shell3.imagegen{ model = "m", api = "openrouter" }
`))
	if ig := c.Imagegen(); ig == nil || ig.API != "openrouter" {
		t.Fatalf("imagegen = %+v, want API=openrouter", c.Imagegen())
	}
}

func TestImagegenBadAPI(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", mediaConfig(`
shell3.imagegen{ model = "m", api = "bogus" }
`))
	_, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	for _, want := range []string{"openai", "openrouter"} {
		if !contains(err.Error(), want) {
			t.Fatalf("error should list openai|openrouter, got: %v", err)
		}
	}
}

func TestMediaBadKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", mediaConfig(`
shell3.stt{ modle = "m" }
`))
	_, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !contains(err.Error(), "unknown key") || !contains(err.Error(), "modle") {
		t.Fatalf("want mustKeys-style error, got: %v", err)
	}
}

func TestTTSEchoFalse(t *testing.T) {
	c := mustLoad(t, mediaConfig(`
shell3.stt{ model = "m", echo = false }
`))
	s := c.STT()
	if s == nil || s.Echo {
		t.Fatalf("stt = %+v, want Echo=false", s)
	}
}
