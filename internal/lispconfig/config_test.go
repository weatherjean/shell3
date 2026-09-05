package lispconfig

import (
	"strings"
	"testing"
	"time"
)

const validConfig = `(shell3
  (version 1)
  (memory "Prefer concise reports.")
  (define codex-bin "codex")

  (model primary
    (base-url "https://example.test/v1")
    (api-key-env TEST_API_KEY)
    (id "test-model")
    (reasoning high)
    (max-tokens 12000)
    (context-window 100000))

  (orchestrator
    (model primary)
    (prompt "Coordinate work with wrk files."))

  (skill testing
    (description "Use when verification is required.")
    (instructions "Run focused tests, then broaden."))

  (telegram
    (token-env TEST_TELEGRAM_TOKEN)
    (home-chat 123456789)
    (allow-from 123456789 987654321)
    (max-concurrent-turns 3)
    (group-messages addressed))

  (schedule daily-report
    (cron "0 8 * * *")
    (timezone "Europe/Ljubljana")
    (run (wrkfile "workflows/daily-report.wrk.lisp"))
    (request "Produce the daily report.")
    (output "report.md")
    (timeout "30m")
    (overlap skip)
    (notify "main"))

  (runner codex
    (parameters
      (model string required)
      (profile string optional "automation"))
    (command codex-bin "exec")
    (arguments
      "--json"
      "--output-last-message" result-file
      "--cd" workdir
      "--model" model
      (optional profile "--profile" profile)
      "-")
    (stderr log)
    (result (file result-file))
    (success (exit 0))
    (timeout "30m"))

  (agent builder
    (using codex)
    (model "gpt-test")))
`

func TestParseResolvesRunnerAndAgent(t *testing.T) {
	cfg, err := Parse("shell3.lisp", []byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Main == nil || cfg.Main.Model != "primary" || cfg.Models["primary"].ContextWindow != 100000 {
		t.Fatalf("orchestrator config = %+v / %+v", cfg.Main, cfg.Models)
	}
	if cfg.Memory != "Prefer concise reports." || len(cfg.Skills) != 1 || cfg.Skills[0].Name != "testing" {
		t.Fatalf("embedded resources = %q / %+v", cfg.Memory, cfg.Skills)
	}
	if cfg.Telegram == nil || cfg.Telegram.TokenEnv != "TEST_TELEGRAM_TOKEN" || cfg.Telegram.HomeChat != 123456789 ||
		cfg.Telegram.MaxConcurrentTurns != 3 || len(cfg.Telegram.AllowFrom) != 2 || cfg.Telegram.GroupMessages != "addressed" {
		t.Fatalf("telegram config = %+v", cfg.Telegram)
	}
	if len(cfg.Schedules) != 1 || cfg.Schedules[0].Name != "daily-report" || cfg.Schedules[0].Timezone != "Europe/Ljubljana" ||
		cfg.Schedules[0].Wrkfile != "workflows/daily-report.wrk.lisp" || cfg.Schedules[0].Output != "report.md" ||
		cfg.Schedules[0].Timeout != 30*time.Minute || cfg.Schedules[0].Overlap != "skip" || cfg.Schedules[0].Notify != "main" {
		t.Fatalf("schedule = %+v", cfg.Schedules)
	}
	r := cfg.Runners["codex"]
	if len(r.Command) != 2 || r.Command[0].Literal != "codex" || r.Command[1].Literal != "exec" {
		t.Fatalf("command = %+v", r.Command)
	}
	if r.Result != "file" || r.Timeout != 30*time.Minute {
		t.Fatalf("runner protocol = %+v", r)
	}
	if got := cfg.Agents["builder"]; got.Runner != "codex" || got.Parameters["model"] != "gpt-test" || got.Parameters["profile"] != "automation" {
		t.Fatalf("agent = %+v", got)
	}
}

func TestParseScheduleDefaults(t *testing.T) {
	cfg, err := Parse("shell3.lisp", []byte(`(shell3 (version 1)
  (schedule daily
    (cron "0 8 * * *")
    (timezone "UTC")
    (run (wrkfile "daily.wrk.lisp"))
    (output "report.txt")
    (timeout "1m")))`))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.Schedules[0]
	if got.Request != "" || got.Overlap != "skip" || got.Notify != "main" {
		t.Fatalf("schedule defaults = request %q, overlap %q, notify %q", got.Request, got.Overlap, got.Notify)
	}
}

func TestParseRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{name: "unknown root", src: `(shell3 (version 1) (mcp nope))`, want: `unknown shell3 form "mcp"`},
		{name: "duplicate", src: `(shell3 (version 1) (define x "a") (define x "b"))`, want: `duplicate constant "x"`},
		{name: "unknown command symbol", src: `(shell3 (version 1) (runner r (command nope) (result stdout)))`, want: `unknown argument symbol "nope"`},
		{name: "unknown runner", src: `(shell3 (version 1) (agent a (using missing)))`, want: `uses unknown runner "missing"`},
		{name: "missing parameter", src: `(shell3 (version 1) (runner r (parameters (model string required)) (command "run") (result stdout)) (agent a (using r)))`, want: `missing required runner parameter "model"`},
		{name: "unknown main model", src: `(shell3 (version 1) (orchestrator (model missing) (prompt "test")))`, want: `orchestrator uses unknown model "missing"`},
		{name: "missing main prompt", src: `(shell3 (version 1) (model m (api-key-env KEY) (id "x")) (orchestrator (model m)))`, want: `orchestrator is missing prompt`},
		{name: "unknown orchestrator field", src: `(shell3 (version 1) (model m (api-key-env KEY) (id "x")) (orchestrator (model m) (instructions "old")))`, want: `unknown orchestrator field "instructions"`},
		{name: "unknown model field", src: `(shell3 (version 1) (model m (provider openai-compatible) (api-key-env KEY) (id "x")))`, want: `unknown model field "provider"`},
		{name: "unknown runner stdin field", src: `(shell3 (version 1) (runner r (command "run") (stdin prompt-file) (result stdout)))`, want: `unknown runner field "stdin"`},
		{name: "unknown runner stdout field", src: `(shell3 (version 1) (runner r (command "run") (stdout text) (result stdout)))`, want: `unknown runner field "stdout"`},
		{name: "unknown agent parameter", src: `(shell3 (version 1) (runner r (command "run") (result stdout)) (agent a (using r) (instructions "task")))`, want: `unknown parameter "instructions"`},
		{name: "incomplete skill", src: `(shell3 (version 1) (skill web (description "search")))`, want: `skill "web" is missing instructions`},
		{name: "duplicate memory", src: `(shell3 (version 1) (memory "one") (memory "two"))`, want: `duplicate memory`},
		{name: "missing model secret ref", src: `(shell3 (version 1) (model m (id "x")))`, want: `model "m" is missing api-key-env`},
		{name: "unknown model field", src: `(shell3 (version 1) (model m (api-key-env KEY) (id "x") (magic true)))`, want: `unknown model field "magic"`},
		{name: "telegram missing token", src: `(shell3 (version 1) (telegram (home-chat 1)))`, want: `telegram is missing token-env`},
		{name: "telegram group without operator", src: `(shell3 (version 1) (telegram (token-env TOKEN) (home-chat -1001)))`, want: `allow-from must name an operator`},
		{name: "telegram unknown field", src: `(shell3 (version 1) (telegram (token-env TOKEN) (home-chat 1) (magic yes)))`, want: `unknown telegram field "magic"`},
		{name: "schedule missing timezone", src: `(shell3 (version 1) (schedule daily (cron "0 8 * * *") (run (wrkfile "daily.wrk.lisp")) (output "out") (timeout "1m")))`, want: `missing timezone`},
		{name: "schedule invalid cron", src: `(shell3 (version 1) (schedule daily (cron "tomorrow") (timezone "UTC") (run (wrkfile "daily.wrk.lisp")) (output "out") (timeout "1m")))`, want: `invalid cron`},
		{name: "schedule arbitrary command", src: `(shell3 (version 1) (schedule daily (cron "0 8 * * *") (timezone "UTC") (run "rm -rf nope") (output "out") (timeout "1m")))`, want: `run requires (wrkfile PATH)`},
		{name: "schedule escaping wrkfile", src: `(shell3 (version 1) (schedule daily (cron "0 8 * * *") (timezone "UTC") (run (wrkfile "../daily.wrk.lisp")) (output "out") (timeout "1m")))`, want: `wrkfile must be relative`},
		{name: "schedule escaping output", src: `(shell3 (version 1) (schedule daily (cron "0 8 * * *") (timezone "UTC") (run (wrkfile "daily.wrk.lisp")) (output "../out") (timeout "1m")))`, want: `output must be relative`},
		{name: "schedule missing timeout", src: `(shell3 (version 1) (schedule daily (cron "0 8 * * *") (timezone "UTC") (run (wrkfile "daily.wrk.lisp")) (output "out")))`, want: `missing timeout`},
		{name: "duplicate schedule", src: `(shell3 (version 1) (schedule daily (cron "0 8 * * *") (timezone "UTC") (run (wrkfile "a.wrk.lisp")) (output "out") (timeout "1m")) (schedule daily (cron "0 9 * * *") (timezone "UTC") (run (wrkfile "b.wrk.lisp")) (output "out") (timeout "1m")))`, want: `duplicate schedule "daily"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse("bad.lisp", []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
