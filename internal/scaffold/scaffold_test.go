package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
)

func writeEnv(t *testing.T, dir string) {
	t.Helper()
	body := "MAIN_API_KEY=\nTELEGRAM_TOKEN=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderBaseConfig(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "kimi-k2.6", Proxy: ""}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	for _, want := range []string{
		"  main:",
		`base_url: "http://localhost:8787/v1"`,
		"api_key: env:MAIN_API_KEY",
		`model: "kimi-k2.6"`,
		`# run_proxy: "npx`,
		"token: env:TELEGRAM_TOKEN",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.sh missing %q", want)
		}
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.sh still contains an unrendered template delimiter")
	}
	agentMD, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	if !strings.Contains(string(agentMD), "model: main") {
		t.Error("shell3.sh should reference the model")
	}
	for _, p := range []string{
		"skills/planning.md", "skills/history.md",
		"skills/self-evolve.md", "skills/browser.md", "skills/scripting.md",
		"skills/cookbook.md", "skills/find-skills.md", "skills/writing-code.md",
		"skills/using-llms.md",
		"lib/bin/skill-search",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestRenderBaseConfigChatID(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:1/v1", EnvKey: "K", Model: "m",
		ChatID: "123456789"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	if !strings.Contains(string(cfg), `chat_id: "123456789"`) {
		t.Errorf("shell3.sh missing quoted chat_id; got:\n%s", cfg)
	}
}

func TestRenderBaseConfigContextWindow(t *testing.T) {
	t.Run("explicit values render through", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", ContextWindow: 200000, CompactAt: 150000}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		for _, want := range []string{"context_window: 200000", "compact_at: 150000"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.sh missing %q", want)
			}
		}
	})

	t.Run("zero values default with compact_at at 80%", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		for _, want := range []string{"context_window: 128000", "compact_at: 102400"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.sh missing defaulted %q", want)
			}
		}
	})
}

func TestRenderedYAMLHasNoMediaBlock(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	for _, banned := range []string{"media:", "stt:", "tts:", "describe:", "imagegen:"} {
		if strings.Contains(string(cfg), banned) {
			t.Errorf("rendered shell3.sh still contains %q", banned)
		}
	}
	if !strings.Contains(string(cfg), "media_keep_days") {
		t.Errorf("rendered shell3.sh dropped media_keep_days")
	}
}

func TestRenderBaseConfigWithProxy(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Proxy: "npx codex-proxy --port 8787"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if !strings.Contains(string(cfg), `run_proxy: "npx codex-proxy --port 8787"`) {
		t.Errorf("proxy not wired into shell3.sh:\n%s", cfg)
	}
}

func TestRenderedConfigLoads(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "test", Proxy: ""}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	writeEnv(t, dir)

	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("rendered config failed to load with empty api_key: %v", err)
	}

	if len(c.Models) < 1 {
		t.Errorf("expected >= 1 model, got %d", len(c.Models))
	}
	src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read kit: %v", err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		t.Fatalf("parse kit: %v", err)
	}
	names := make([]string, len(k.Agents))
	for i, a := range k.Agents {
		names[i] = a.Name
		if i > 0 && a.Desc == "" {
			t.Errorf("employee %q needs a description — it is how the main agent decides to dispatch it", a.Name)
		}
	}
	if len(names) != 3 || names[0] != "main" || names[1] != "auditor" || names[2] != "assistant" {
		t.Fatalf("scaffold kit agents = %v, want main + auditor + assistant", names)
	}

	got := map[string]bool{}
	for _, sk := range config.ScanSkills(filepath.Join(dir, "skills")) {
		got[sk.Name] = true
	}
	for _, want := range []string{"planning", "browser", "find-skills", "writing-code",
		"cookbook", "history", "self-evolve", "scripting", "using-llms", "building-agents"} {
		if !got[want] {
			t.Errorf("scaffold skill %q missing (got %v)", want, got)
		}
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("scaffold config loaded with warnings: %v", c.Warnings())
	}
	if c.HasToolCall() {
		t.Error("a scaffolded config should have no hooks/ dir — the gate is declared in the kit")
	}
}

func TestRenderBaseConfigDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.sh")
	if err := os.WriteFile(cfgPath, []byte("# user edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != "# user edited\n" {
		t.Errorf("RenderBaseConfig clobbered an existing shell3.sh")
	}
}

func TestRenderBaseConfigForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.sh")
	if err := os.WriteFile(cfgPath, []byte("# stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, true); err != nil {
		t.Fatalf("force render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) == "# stale\n" {
		t.Error("force=true did not overwrite shell3.sh")
	}
	if !strings.Contains(string(got), "base_url:") {
		t.Errorf("force render did not regenerate config; got:\n%s", got)
	}
}

func TestRenderBaseConfigEscapesYAMLSpecials(t *testing.T) {
	dir := t.TempDir()
	v := Values{
		Name:    "main",
		BaseURL: `http://x/v1" oops: [`,
		EnvKey:  "MAIN_API_KEY",
		Model:   `weird\model`,
		Proxy:   `sh -c "echo hi"`,
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	writeEnv(t, dir)
	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config with YAML-special inputs failed to load: %v", err)
	}
	m := c.Models[0]
	if m.BaseURL != v.BaseURL {
		t.Errorf("base_url = %q, want %q", m.BaseURL, v.BaseURL)
	}
	if m.ModelID != v.Model {
		t.Errorf("model = %q, want %q", m.ModelID, v.Model)
	}
	if m.RunProxy != v.Proxy {
		t.Errorf("run_proxy = %q, want %q", m.RunProxy, v.Proxy)
	}
}

func TestBaseConfigWiresTheBotToken(t *testing.T) {
	dir := t.TempDir()
	if err := RenderBaseConfig(dir, Values{
		Name: "main", BaseURL: "http://x", EnvKey: "K", Model: "m",
		ContextWindow: 100000, CompactAt: 80000, WorkDir: dir,
	}, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "token: env:TELEGRAM_TOKEN") {
		t.Errorf("shell3.sh does not wire the bot token:\n%s", body)
	}
}

func TestScaffoldCronExampleIsInert(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "test"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read kit: %v", err)
	}
	if !strings.Contains(string(src), "cron: checklist") {
		t.Fatal("the kit template should carry a commented cron: example")
	}
	k, err := kit.Parse(src)
	if err != nil {
		t.Fatalf("parse kit: %v", err)
	}
	for _, j := range k.Crons {
		if j.Name == "checklist" {
			t.Fatalf("the commented example armed itself: %+v", j)
		}
	}
}

func TestScaffoldShipsSelfKnowledgeSkill(t *testing.T) {
	files, err := PromptFiles(Values{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	body, ok := files["skills/self-knowledge.md"]
	if !ok {
		t.Fatal("skills/self-knowledge.md is not shipped — new installs would have no self-knowledge skill")
	}
	for _, want := range []string{
		"cannot see your own internals",
		"status",
		"turn_prompts",
		"you did not see it",
		"absence claim needs a command",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("self-knowledge skill missing %q", want)
		}
	}
}

func TestScaffoldShipsTheAuditor(t *testing.T) {
	dir := t.TempDir()
	if err := RenderBaseConfig(dir, Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}, false); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		t.Fatalf("parse kit: %v", err)
	}
	var auditor *kit.Agent
	for i := range k.Agents {
		if k.Agents[i].Name == "auditor" {
			auditor = &k.Agents[i]
		}
	}
	if auditor == nil {
		t.Fatalf("scaffold kit declares no auditor agent; agents = %+v", k.Agents)
	}
	if auditor.Desc == "" {
		t.Error("the auditor needs a description — it is how the main agent decides to dispatch it")
	}

	var job *kit.CronJob
	for i := range k.Crons {
		if k.Crons[i].Name == "harness-audit" {
			job = &k.Crons[i]
		}
	}
	if job == nil {
		t.Fatalf("scaffold kit declares no harness-audit cron; crons = %+v", k.Crons)
	}
	if job.Agent != "auditor" {
		t.Errorf("harness-audit targets %q, want the auditor", job.Agent)
	}
	if !strings.Contains(job.Prompt, "NO_REPLY") {
		t.Errorf("harness-audit prompt must end a clean run with NO_REPLY, or the audit costs a turn a day:\n%s", job.Prompt)
	}
	for _, want := range []string{"chat/completions", "secrets/", "200 lines"} {
		if !strings.Contains(job.Prompt, want) {
			t.Errorf("harness-audit prompt is missing the %q check:\n%s", want, job.Prompt)
		}
	}
}

func TestAuditPromptJudgesConvertVsDecide(t *testing.T) {
	dir := t.TempDir()
	if err := RenderBaseConfig(dir, Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}, false); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatal(err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		t.Fatalf("parse kit: %v", err)
	}
	var job *kit.CronJob
	for i := range k.Crons {
		if k.Crons[i].Name == "harness-audit" {
			job = &k.Crons[i]
		}
	}
	if job == nil {
		t.Fatalf("scaffold kit declares no harness-audit cron; crons = %+v", k.Crons)
	}
	for _, want := range []string{"CONVERTS", "DECIDES"} {
		if !strings.Contains(job.Prompt, want) {
			t.Errorf("audit prompt must carry the convert/decide criterion; missing %q:\n%s", want, job.Prompt)
		}
	}
	if strings.Contains(job.Prompt, "shell3 ask --agent") {
		t.Error("check 1 still tells a script to shell out to ask --agent, which using-llms rule 1 forbids for a tool")
	}
}

func TestScaffoldShipsTestingWorkflowsSkill(t *testing.T) {
	files, err := PromptFiles(Values{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	body, ok := files["skills/testing-workflows.md"]
	if !ok {
		t.Fatal("skills/testing-workflows.md is not shipped — installs would keep testing tools in isolation")
	}
	if !strings.Contains(string(body), "shell3 ask --agent") {
		t.Error("the skill must name the command that runs a real turn test")
	}
}

func TestUsingLLMsCarriesPerception(t *testing.T) {
	files, err := PromptFiles(Values{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["skills/media.md"]; ok {
		t.Error("media.md was folded into using-llms.md")
	}
	src := string(files["skills/using-llms.md"])
	for _, want := range []string{
		"convert between forms",
		"tool: see",
		"ask the operator",
		"shell3 tool test",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("using-llms.md missing %q", want)
		}
	}
}
