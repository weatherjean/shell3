//go:build unix

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	huh "charm.land/huh/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/scaffold"
)

type bootFlags struct {
	url, model, name, key, proxy, braveKey string
	contextWindow, compactAt               string
	tgToken, tgChatID                      string
	vision                                 bool
	visionSet                              bool // --vision passed explicitly (skips the form's confirm)
	force                                  bool
}

func newBootCommand() *cobra.Command {
	f := &bootFlags{}
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Create a shell3 config interactively (model + Telegram bot)",
		Example: `  shell3 boot
  shell3 boot --url https://api.deepseek.com/v1 --model deepseek-chat --name main \
    --tg-token 123:ABC --tg-chat-id 8701499393`,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().StringVar(&f.braveKey, "brave-key", "", "Optional Brave Search API key")
	cmd.Flags().StringVar(&f.tgToken, "tg-token", "", "Telegram bot token (from @BotFather)")
	cmd.Flags().StringVar(&f.tgChatID, "tg-chat-id", "", "Telegram chat id the bot answers")
	cmd.Flags().BoolVar(&f.vision, "vision", true, "Model can see images (wires shell3.describe to it and enables the media tool)")
	cmd.Flags().BoolVar(&f.force, "force", false, "Overwrite an existing ~/.shell3/shell3.lua")
	return cmd
}

func runBoot(f *bootFlags) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot: home dir: %w", err)
	}
	g := paths.NewGlobal(home)
	dir, cfgPath := g.Root, g.ConfigFile

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

	// The web front-end's shared secret is generated, not prompted: boot
	// always writes SHELL3_WEB_SECRET so the scaffold's shell3.web{} block
	// loads out of the box (mergeEnv keeps an existing value on re-boot).
	webSecret, err := randomHex(24)
	if err != nil {
		return fmt.Errorf("boot: generate web secret: %w", err)
	}

	envPairs := [][2]string{{envKey, a.key}, {"BRAVE_API_KEY", a.braveKey}, {"TELEGRAM_BOT_TOKEN", a.tgToken}, {"SHELL3_WEB_SECRET", webSecret}}

	if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
		Name: a.name, BaseURL: a.url, EnvKey: envKey, Model: a.model, Proxy: a.proxy,
		ContextWindow: a.ctxWindow, CompactAt: a.compactAt, ChatID: a.tgChatID,
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
		// SHELL3_WEB_SECRET is generated fresh each boot (never user-typed), so
		// keeping the existing one is its normal case, not worth a note. Only
		// runBoot knows which pairs came from prompts vs randomHex — the
		// provenance filter lives here, not inside the generic mergeEnv.
		if k == "SHELL3_WEB_SECRET" {
			continue
		}
		fmt.Printf("note: kept the existing %s in %s — edit that file to change it\n", k, envPath)
	}

	printBootSuccess(dir, cfgPath, envPath, a.proxy != "")
	return nil
}

// bootAnswers is the resolved configuration input: flags merged with either
// the interactive huh form (TTY) or defaults (non-TTY).
type bootAnswers struct {
	url, model, name, key string
	proxy, braveKey       string
	tgToken, tgChatID     string
	ctxWindow, compactAt  int
	vision                bool
}

// collectAnswers resolves every boot input. Flags always win; on a TTY the
// remaining fields are asked via a huh form, otherwise they take defaults
// (model is required — boot refuses to guess it headlessly).
func collectAnswers(f *bootFlags, tty bool) (bootAnswers, error) {
	a := bootAnswers{
		url: f.url, model: f.model, name: f.name, key: f.key,
		proxy: f.proxy, braveKey: f.braveKey,
		tgToken: f.tgToken, tgChatID: f.tgChatID,
		vision: f.vision,
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
			huh.NewConfirm().Title("Can this model see images (vision)?").
				Description("Yes: inbound Telegram images are captioned by this model and\nthe read_media tool is enabled. No: media tooling stays off until\nyou add a vision model.").
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
	if f.braveKey == "" {
		extras = append(extras, huh.NewInput().Title("Brave Search key").
			Description("Enables the brave_search tool. Blank to add later.").Value(&a.braveKey))
	}
	if len(extras) > 0 {
		groups = append(groups, huh.NewGroup(extras...).Title("Extras"))
	}

	var tg []huh.Field
	if f.tgToken == "" {
		tg = append(tg, huh.NewInput().Title("Bot token").
			Description("From @BotFather. Blank to fill in shell3.telegram{} later.").Value(&a.tgToken))
	}
	if f.tgChatID == "" {
		tg = append(tg, huh.NewInput().Title("Chat id").
			Description("Your numeric chat id (e.g. from @userinfobot).").
			Value(&a.tgChatID))
	}
	if len(tg) > 0 {
		groups = append(groups, huh.NewGroup(tg...).
			Title("Telegram").
			Description("shell3 talks to you over a Telegram bot."))
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

// randomHex returns n random bytes hex-encoded (2n characters).
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// mergeEnv appends each kv pair absent from existing (existing values
// untouched); result ends with a newline. kept reports every key whose
// incoming value was non-empty but discarded because the key already exists —
// so a re-boot can tell the user their freshly typed secret was NOT applied
// instead of silently keeping the stale one. Callers filter out keys they
// generated themselves (see runBoot's SHELL3_WEB_SECRET filter).
func mergeEnv(existing string, kv [][2]string) (merged string, kept []string) {
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
			if pair[1] != "" {
				kept = append(kept, pair[0])
			}
			continue
		}
		if pair[0] == "BRAVE_API_KEY" && pair[1] == "" {
			b.WriteString("# Brave Search API key — fill in to enable the brave_search tool.\n")
		}
		if pair[0] == "TELEGRAM_BOT_TOKEN" && pair[1] == "" {
			b.WriteString("# Telegram bot token from @BotFather — fill in before `shell3 telegram`.\n")
		}
		b.WriteString(pair[0] + "=" + pair[1] + "\n")
	}
	return b.String(), kept
}

func printBootSuccess(dir, cfgPath, envPath string, proxyWired bool) {
	fmt.Println()
	fmt.Println("shell3 is configured.")
	fmt.Printf("  config:  %s\n", cfgPath)
	fmt.Printf("  modules: %s\n", filepath.Join(dir, "lib"))
	fmt.Printf("  secrets: %s  (never commit this)\n", envPath)
	if proxyWired {
		fmt.Println("  proxy:   run_proxy wired — shell3 starts it when the model is first used.")
	} else {
		fmt.Println("  proxy:   none. If your endpoint is a proxy you launch (e.g. a Codex")
		fmt.Println("           subscription via `npx ...`), add run_proxy to the model block.")
	}
	fmt.Println()
	fmt.Printf("Take a minute to open %s and look it over —\n", cfgPath)
	fmt.Println("the model block (context_window, compact_at) and the bash safety hook")
	fmt.Println("are worth a glance before your first run. Some models also need a")
	fmt.Println("provider-specific `extra = { ... }` field (e.g. MiniMax wants")
	fmt.Println("reasoning_split = true) — there's a commented example in the model block.")
	fmt.Println()
	fmt.Println("Edit shell3.lua (and lib/) to add tools, skills, or agents —")
	fmt.Println("recipes live in the shell3 repo under docs/cookbook/.")
	fmt.Println()
	fmt.Println("shell3 talks to you over Telegram. Make sure TELEGRAM_BOT_TOKEN is set")
	fmt.Println("in .env and chat_id is filled in shell3.telegram{}, then run:")
	fmt.Println()
	fmt.Println("Run:  shell3 telegram")
	fmt.Println()
	fmt.Println("No Telegram? `shell3 web` serves the same dashboard plus a browser chat;")
	fmt.Println("it prints a ready-to-open URL (the generated SHELL3_WEB_SECRET as ?key=).")
	fmt.Println()
	fmt.Println("The Mini App dashboard is exposed through a cloudflared tunnel by")
	fmt.Println("default (free, no account) — install it so the dashboard is reachable")
	fmt.Println("from your phone (e.g. `brew install cloudflared` on macOS):")
	fmt.Println("  https://github.com/cloudflare/cloudflared")
	fmt.Println("Not installed? The bot still runs; the dashboard just stays local.")
	fmt.Println("Prefer another tunnel? Edit dashboard.tunnel/url in shell3.lua.")
	if _, err := exec.LookPath("cloudflared"); err != nil {
		fmt.Println()
		fmt.Println("NOTE: cloudflared was not found on PATH on this machine.")
	}
}
