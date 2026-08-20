//go:build unix

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/scaffold"
)

type bootFlags struct {
	url, model, name, key, proxy string
	contextWindow, compactAt     string
	tgToken, tgChatID            string
	workDir                      string
	vision                       bool
	visionSet                    bool // --vision passed explicitly (skips the form's confirm)
	force                        bool
	show                         bool // print the post-boot summary and exit
	prompts                      bool // refresh scaffold prompt files and exit
}

func newBootCommand() *cobra.Command {
	f := &bootFlags{}
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Create a shell3 config interactively (model + Telegram bot)",
		Example: `  shell3 boot
  shell3 boot --url https://api.deepseek.com/v1 --model deepseek-chat --name main \
    --tg-token 123:ABC --tg-chat-id 123456789 --workdir ~/work`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if f.show {
				return showBootSuccess()
			}
			if f.prompts {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("boot: home dir: %w", err)
				}
				return runPromptRefresh(paths.NewGlobal(home).Root, time.Now())
			}
			f.visionSet = cmd.Flags().Changed("vision")
			return runBoot(f)
		},
	}
	cmd.Flags().StringVar(&f.url, "url", "", "Base URL (OpenAI-compatible endpoint)")
	cmd.Flags().StringVar(&f.model, "model", "", "Model tag/id")
	cmd.Flags().StringVar(&f.name, "name", "", "Handle for this model (default: main)")
	cmd.Flags().StringVar(&f.key, "key", "", "API key")
	cmd.Flags().StringVar(&f.proxy, "proxy", "", "Optional run_proxy command")
	cmd.Flags().StringVar(&f.contextWindow, "context-window", "", "Model context window in tokens (default 128000)")
	cmd.Flags().StringVar(&f.compactAt, "compact-at", "", "Auto-compaction threshold in tokens (default 80% of context window)")
	cmd.Flags().StringVar(&f.tgToken, "tg-token", "", "Telegram bot token (from @BotFather)")
	cmd.Flags().StringVar(&f.tgChatID, "tg-chat-id", "", "Telegram chat id the bot answers")
	cmd.Flags().StringVar(&f.workDir, "workdir", "", "Where the agent's shell runs (default: the config dir)")
	cmd.Flags().BoolVar(&f.vision, "vision", true, "Model can see images (adds the read_media tool)")
	cmd.Flags().BoolVar(&f.force, "force", false, "Overwrite an existing ~/.shell3 config (shell3.sh, skills/, ...)")
	cmd.Flags().BoolVar(&f.show, "show", false, "Print the post-boot summary for the existing config and exit (changes nothing)")
	cmd.Flags().BoolVar(&f.prompts, "prompts", false,
		"Refresh the scaffold's prompt files (agent.md body, agents/, skills/) in an existing config, backing up replaced files to .backup/")
	return cmd
}

func runBoot(f *bootFlags) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot: home dir: %w", err)
	}
	g := paths.NewGlobal(home)
	dir := g.Root
	cfgPath := filepath.Join(dir, "shell3.sh")

	if _, err := os.Stat(cfgPath); err == nil && !f.force {
		return fmt.Errorf("boot: %s already exists — pass --force to overwrite", cfgPath)
	}

	// The huh form needs a terminal on both ends: it reads keys from stdin and
	// renders its TUI to stdout (a piped stdout would capture control codes).
	tty := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	a, err := collectAnswers(f, tty)
	if err != nil {
		return err
	}

	envKey := envKeyForName(a.name)

	envPairs := [][2]string{{envKey, a.key}, {envTelegramToken, a.tgToken}}

	if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
		Name: a.name, BaseURL: a.url, EnvKey: envKey, Model: a.model, Proxy: a.proxy,
		ContextWindow: a.ctxWindow, CompactAt: a.compactAt, WorkDir: a.workDir,
		ChatID: a.tgChatID,
		Vision: a.vision,
	}, f.force); err != nil {
		return err
	}

	envPath := filepath.Join(dir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("boot: read .env: %w", err)
	}
	merged, kept := mergeEnv(string(existing), envPairs)
	if err := atomicWriteFile(envPath, []byte(merged), 0o600); err != nil {
		return fmt.Errorf("boot: write .env: %w", err)
	}
	for _, k := range kept {
		fmt.Printf("note: kept the existing %s in %s — edit that file to change it\n", k, envPath)
	}

	printBootSuccess(dir, cfgPath, envPath, a.proxy != "")
	return nil
}

// showBootSuccess reprints the post-boot summary for the existing config —
// the same message boot ends on, re-derived from what's on disk (nothing is
// written or asked). Handy after the original ran off the top of the terminal.
func showBootSuccess() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot: home dir: %w", err)
	}
	dir := paths.NewGlobal(home).Root
	cfgPath := filepath.Join(dir, "shell3.sh")
	yaml, err := os.ReadFile(cfgPath)
	if err != nil {
		return fmt.Errorf("boot --show: no config at %s — run `shell3 boot` first", cfgPath)
	}

	// Re-derive the message variant from disk: an uncommented run_proxy line
	// means a proxy is wired.
	proxyWired := false
	for _, line := range strings.Split(string(yaml), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "run_proxy:") {
			proxyWired = true
			break
		}
	}
	printBootSuccess(dir, cfgPath, filepath.Join(dir, ".env"), proxyWired)
	return nil
}

// bootAnswers is the resolved configuration input: flags merged with either
// the interactive huh form (TTY) or defaults (non-TTY).
type bootAnswers struct {
	url, model, name, key string
	proxy                 string
	tgToken, tgChatID     string
	workDir               string
	ctxWindow, compactAt  int
	vision                bool
}

// collectAnswers resolves every boot input. Flags always win; on a TTY the
// remaining fields are asked via a huh form, otherwise they take defaults
// (model is required — boot refuses to guess it headlessly).
func collectAnswers(f *bootFlags, tty bool) (bootAnswers, error) {
	a := bootAnswers{
		url: f.url, model: f.model, name: f.name, key: f.key,
		proxy:    f.proxy,
		tgToken:  f.tgToken,
		tgChatID: f.tgChatID,
		workDir:  f.workDir,
		vision:   f.vision,
	}
	ctxStr, compactStr := f.contextWindow, f.compactAt

	if tty {
		if err := runBootForm(f, &a, &ctxStr, &compactStr); err != nil {
			return a, err
		}
	} else if a.model == "" {
		return a, fmt.Errorf("boot: --model required when not running in a terminal")
	}
	if a.url == "" {
		a.url = defaultBaseURL
	}
	if a.name == "" {
		a.name = "main"
	}
	// A flag-supplied chat id skipped the form's validator; catch it here so a
	// bad value never reaches shell3.yaml.
	a.tgChatID = strings.TrimSpace(a.tgChatID)
	if err := validateChatID(a.tgChatID); err != nil {
		return a, fmt.Errorf("boot: chat id %q: %w", a.tgChatID, err)
	}

	var err error
	if a.ctxWindow, err = positiveInt(ctxStr, scaffold.DefaultContextWindow, "context window"); err != nil {
		return a, err
	}
	// Blank compact-at means the 80% default (headroom for the post-compaction turn).
	if a.compactAt, err = positiveInt(compactStr, a.ctxWindow*80/100, "auto-compact threshold"); err != nil {
		return a, err
	}
	return a, nil
}

const defaultBaseURL = "https://api.openai.com/v1"

// runBootForm asks for every field not already provided as a flag, one huh
// group per topic. It mutates a (and the two int fields' string staging) in
// place; a Ctrl-C surfaces as a plain "aborted" error.
func runBootForm(f *bootFlags, a *bootAnswers, ctxStr, compactStr *string) error {
	var groups []*huh.Group

	var model []huh.Field
	if f.url == "" {
		a.url = defaultBaseURL
		model = append(model, huh.NewInput().Title("Base URL").
			Description("OpenAI-compatible endpoint.").Value(&a.url))
	}
	if f.model == "" {
		model = append(model, huh.NewInput().Title("Model tag").
			Placeholder("e.g. deepseek-chat").Validate(huh.ValidateNotEmpty()).Value(&a.model))
	}
	if f.name == "" {
		a.name = "main"
		model = append(model, huh.NewInput().Title("Name").
			Description("shell3's handle for this model.").Value(&a.name))
	}
	// Secrets echo visibly on purpose: boot runs on a local terminal, and a
	// long pasted token you can't see is a truncated paste waiting to happen.
	if f.key == "" {
		model = append(model, huh.NewInput().Title("API key").
			Description("Blank if your proxy handles auth.").Value(&a.key))
	}
	if len(model) > 0 {
		groups = append(groups, huh.NewGroup(model...).Title("Model"))
	}

	if !f.visionSet {
		a.vision = true
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().Title("Can your model see images?").
				Description("Yes: adds the read_media tool, so the agent can open an image,\naudio, or PDF file directly. No: leave it off until you\nswitch to a multimodal model.").
				Value(&a.vision),
		).Title("Vision"))
	}

	var ctx []huh.Field
	if f.contextWindow == "" {
		*ctxStr = strconv.Itoa(scaffold.DefaultContextWindow)
		ctx = append(ctx, huh.NewInput().Title("Context window (tokens)").
			Description("Your model's real token budget — the wrong value skews\ncontext-usage reminders and auto-compaction.").
			Validate(validatePositiveInt(false)).Value(ctxStr))
	}
	if f.compactAt == "" {
		ctx = append(ctx, huh.NewInput().Title("Auto-compact at (tokens)").
			Description("Blank = 80% of the context window.").
			Placeholder("blank for 80%").
			Validate(validatePositiveInt(true)).Value(compactStr))
	}
	if len(ctx) > 0 {
		groups = append(groups, huh.NewGroup(ctx...).Title("Context"))
	}

	var extras []huh.Field
	if f.proxy == "" {
		extras = append(extras, huh.NewInput().Title("Proxy command").
			Description("Some endpoints are a proxy you launch yourself (e.g. a Codex\nsubscription fronted by `npx ...`); shell3 auto-starts it on\nactivation. Blank to skip.").
			Value(&a.proxy))
	}
	if len(extras) > 0 {
		groups = append(groups, huh.NewGroup(extras...).Title("Extras"))
	}

	// Secrets echo visibly here too: a bot token is long, and a paste you
	// cannot see is a truncated paste waiting to happen.
	var tg []huh.Field
	if f.tgToken == "" {
		tg = append(tg, huh.NewInput().Title("Bot token").
			Description("From @BotFather. Blank to fill into .env later.").Value(&a.tgToken))
	}
	if f.tgChatID == "" {
		tg = append(tg, huh.NewInput().Title("Chat id").
			Description("Your numeric chat id (e.g. from @userinfobot).").
			Validate(validateChatID).
			Value(&a.tgChatID))
	}
	if len(tg) > 0 {
		groups = append(groups, huh.NewGroup(tg...).
			Title("Telegram").
			Description("shell3 talks to you over a Telegram bot."))
	}

	if f.workDir == "" {
		groups = append(groups, huh.NewGroup(
			huh.NewInput().Title("Working directory").
				Description("Where the agent's shell runs. Blank uses the config dir.").
				Value(&a.workDir),
		).Title("Agent").
			Description("Where the agent works when nothing else says otherwise."))
	}

	if len(groups) == 0 {
		return nil // every field came from a flag
	}
	if err := huh.NewForm(groups...).WithTheme(cli.HuhTheme()).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return fmt.Errorf("boot: aborted")
		}
		return fmt.Errorf("boot: %w", err)
	}
	return nil
}

// envTelegramToken is the .env key holding the bot token, referenced from
// shell3.yaml as `telegram.token: env:TELEGRAM_TOKEN` like every other secret.
const envTelegramToken = "TELEGRAM_TOKEN"

// validateChatID keeps a mistyped chat id out of shell3.yaml: the front-end
// parses it (parseChatID, the shared definition) and refuses to start
// otherwise, and that failure lands far from where the value was typed. Blank
// is allowed — it's the "fill it in later" answer.
func validateChatID(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if _, err := parseChatID(s); err != nil {
		return fmt.Errorf("a chat id is a number (e.g. 123456789)")
	}
	return nil
}

// validatePositiveInt validates a form int field via the same parse the
// final positiveInt pass uses; blankOK admits "" (the caller substitutes a
// default).
func validatePositiveInt(blankOK bool) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" {
			if blankOK {
				return nil
			}
			return fmt.Errorf("required")
		}
		if _, err := positiveInt(s, 0, "value"); err != nil {
			return fmt.Errorf("must be a positive integer")
		}
		return nil
	}
}

// positiveInt parses a staged int value: blank takes def, anything else must
// be a positive integer (flag values arrive unvalidated by the form). The
// single definition of "valid" for both the form validator above and the
// final parse.
func positiveInt(s string, def int, label string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("boot: %s must be a positive integer, got %q", label, s)
	}
	return n, nil
}

// atomicWriteFile writes data to path via a temp file in the same directory
// followed by a rename, so a crash mid-write cannot truncate or corrupt an
// existing file — it either has the old contents or the new ones. Used for the
// .env credentials file. The temp file is created 0600; mode is applied before
// the rename. The deferred Remove is a no-op once the rename succeeds.
func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return err
	}
	// Sync before rename, or a power loss can leave the renamed file empty on
	// some filesystems — exactly the corruption this helper promises to prevent.
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// envKeyForName derives the .env key for a model handle: upper-cased, non-alnum
// runs collapsed to "_", suffixed _API_KEY. Empty falls back to MAIN; a leading
// digit is prefixed with "_".
func envKeyForName(name string) string {
	s := nonAlnum.ReplaceAllString(strings.ToUpper(name), "_")
	s = strings.Trim(s, "_")
	if s == "" {
		s = "MAIN"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	return s + "_API_KEY"
}

var nonAlnum = regexp.MustCompile(`[^A-Z0-9]+`)

// mergeEnv appends each kv pair absent from existing (existing values
// untouched); result ends with a newline. kept reports every key whose
// incoming value was non-empty but discarded because the key already exists —
// so a re-boot can tell the user their freshly typed secret was NOT applied
// instead of silently keeping the stale one.
func mergeEnv(existing string, kv [][2]string) (merged string, kept []string) {
	have := envKeySet(existing)
	// A key that exists but holds NOTHING is the "fill it in later" line boot
	// itself writes for a deferred token or API key. Keeping that over a
	// freshly typed value would discard the credential AND report it as kept,
	// so a blank-valued key is filled in place — in place, because appending a
	// second line for the same key leaves the file with two.
	filled := map[string]bool{}
	lines := strings.Split(existing, "\n")
	for i, line := range lines {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.HasPrefix(strings.TrimSpace(line), "#") || strings.TrimSpace(v) != "" {
			continue
		}
		k = strings.TrimSpace(strings.TrimPrefix(k, "export "))
		for _, pair := range kv {
			if pair[0] == k && pair[1] != "" {
				lines[i] = pair[0] + "=" + pair[1]
				filled[k] = true
			}
		}
	}
	existing = strings.Join(lines, "\n")

	var b strings.Builder
	b.WriteString(existing)
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		b.WriteString("\n")
	}
	if existing == "" {
		b.WriteString("# shell3 secrets — never commit this file.\n")
	}
	for _, pair := range kv {
		if have[pair[0]] {
			// Report only a value genuinely discarded — not one just filled
			// into a blank line, and not a blank incoming value (a re-boot
			// that had nothing new to apply).
			if pair[1] != "" && !filled[pair[0]] {
				kept = append(kept, pair[0])
			}
			continue
		}
		if pair[0] == envTelegramToken && pair[1] == "" {
			b.WriteString("# Telegram bot token from @BotFather — fill in before `shell3 telegram`.\n")
		}
		b.WriteString(pair[0] + "=" + pair[1] + "\n")
	}
	return b.String(), kept
}

// envKeySet reports which keys an .env file already defines. Comment and
// malformed lines are skipped; an `export KEY=…` line counts as KEY.
func envKeySet(existing string) map[string]bool {
	have := map[string]bool{}
	for _, line := range strings.Split(existing, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, _, ok := strings.Cut(line, "="); ok {
			have[strings.TrimSpace(strings.TrimPrefix(k, "export "))] = true
		}
	}
	return have
}

func printBootSuccess(dir, cfgPath, envPath string, proxyWired bool) {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# shell3 is configured")
	w("")
	w("Your config is the directory itself:")
	w("")
	w("- **everything** — `%s`: the wiring, every agent, their tools, and the gate", cfgPath)
	w("- **skills** — `%s`", filepath.Join(dir, "skills"))
	w("- **secrets** — `%s` (never commit this)", envPath)
	if proxyWired {
		w("- **proxy** — `run_proxy` wired; started when the model is first used")
	}
	w("")
	w("Worth a glance before first run: the model block (`context_window`,")
	w("`compact_at`), and the `gate:` block — your command gate (ships armed;")
	w("read it and tune it). Some providers need an `extra: { ... }` field")
	w("(e.g. MiniMax wants `reasoning_split: true`).")
	w("")
	w("Add an agent, a tool or a skill by editing that one file, then")
	w("`shell3 tool check %s` and reload — recipes live in the repo", cfgPath)
	w("under `docs/cookbook/`.")
	w("")

	w("## Run it")
	w("")
	w("shell3 talks to you over Telegram. With `%s` in `.env` and", envTelegramToken)
	w("`chat_id` in the wiring block:")
	w("")
	w("```")
	w("shell3 telegram")
	w("```")
	w("")
	w("Then message your bot.")
	w("")
	w("**Prefer the terminal?** `shell3 ask \"hi\"` drives the same agent with")
	w("full verbose output — every tool call, result, and token count")
	w("(`--resume` continues the last session). The fastest way to check the")
	w("config works and watch what the agent does.")
	w("")
	w("## Keeping it up, and reaching it")
	w("")
	w("Nothing to expose: Telegram already reaches your phone, so shell3 stays")
	w("on this machine and talks out through the bot. Running it as a service")
	w("is yours to set up — see `docs/deploying.md` in the repo, or ask the agent.")

	fmt.Println()
	fmt.Print(cli.RenderMarkdown(b.String()))
}
