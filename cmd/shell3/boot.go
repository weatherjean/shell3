//go:build unix

package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/scaffold"
)

type bootFlags struct {
	url, model, name, key, proxy, braveKey string
	contextWindow, compactAt               string
	tgToken, tgChatID                      string
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
		RunE: func(cmd *cobra.Command, args []string) error { return runBoot(f) },
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

	in := bufio.NewReader(os.Stdin)
	tty := term.IsTerminal(int(os.Stdin.Fd()))

	url, err := value(f.url, "Base URL (OpenAI API compatible)", "https://api.openai.com/v1", in, tty, false)
	if err != nil {
		return err
	}
	model, err := value(f.model, "Model tag", "", in, tty, true)
	if err != nil {
		return err
	}
	name, err := value(f.name, "Name (handle for this model)", "main", in, tty, false)
	if err != nil {
		return err
	}
	key, err := value(f.key, "API key (blank if your proxy handles auth)", "", in, tty, false)
	if err != nil {
		return err
	}

	if tty {
		fmt.Println()
		fmt.Println("Context window + compaction are model-specific. Set the context window")
		fmt.Println("to your model's real token budget; the wrong value skews context-usage")
		fmt.Println("reminders and when shell3 auto-compacts the conversation.")
	}
	ctxWindow, err := intValue(f.contextWindow, "Context window (tokens)", scaffold.DefaultContextWindow, in, tty)
	if err != nil {
		return err
	}
	// Default compact threshold to 80% of the context window (headroom for the next turn).
	compactAt, err := intValue(f.compactAt, "Auto-compact at (tokens)", ctxWindow*80/100, in, tty)
	if err != nil {
		return err
	}

	if tty {
		fmt.Println()
		fmt.Println("Local proxy? Some endpoints are a proxy you launch yourself —")
		fmt.Println("e.g. a Codex subscription fronted by `npx ...`.")
		fmt.Println("shell3 can auto-start it on activation (run_proxy).")
	}
	proxy, err := value(f.proxy, "Proxy command (blank to skip)", "", in, tty, false)
	if err != nil {
		return err
	}
	braveKey, err := value(f.braveKey, "Brave Search key (blank to add later)", "", in, tty, false)
	if err != nil {
		return err
	}

	if tty {
		fmt.Println()
		fmt.Println("Telegram: shell3 talks to you over a bot. Create one with @BotFather")
		fmt.Println("to get a token, and get your numeric chat id (e.g. from @userinfobot).")
		fmt.Println("Leave blank to fill in shell3.telegram{} later.")
	}
	tgToken, err := value(f.tgToken, "Telegram bot token (blank to add later)", "", in, tty, false)
	if err != nil {
		return err
	}
	tgChatID, err := value(f.tgChatID, "Telegram chat id", "", in, tty, false)
	if err != nil {
		return err
	}

	envKey := envKeyForName(name)

	envPairs := [][2]string{{envKey, key}, {"BRAVE_API_KEY", braveKey}, {"TELEGRAM_BOT_TOKEN", tgToken}}

	if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
		Name: name, BaseURL: url, EnvKey: envKey, Model: model, Proxy: proxy,
		ContextWindow: ctxWindow, CompactAt: compactAt, ChatID: tgChatID,
	}, f.force); err != nil {
		return err
	}

	envPath := filepath.Join(dir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("boot: read .env: %w", err)
	}
	merged := mergeEnv(string(existing), envPairs)
	if err := atomicWriteFile(envPath, []byte(merged), 0o600); err != nil {
		return fmt.Errorf("boot: write .env: %w", err)
	}

	printBootSuccess(dir, cfgPath, envPath, proxy != "")
	return nil
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
// untouched); result ends with a newline.
func mergeEnv(existing string, kv [][2]string) string {
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
	return b.String()
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

// intValue reads a positive-integer config value: flag wins, else prompt (TTY)
// or def. Never required.
func intValue(flag, label string, def int, in *bufio.Reader, tty bool) (int, error) {
	raw, err := value(flag, label, strconv.Itoa(def), in, tty, false)
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("boot: %s must be a positive integer, got %q", label, raw)
	}
	return n, nil
}

// value reads a config value: flag wins; else prompt (TTY) with optional
// default; errors when required and unavailable.
func value(flag, label, def string, in *bufio.Reader, tty, required bool) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !tty {
		if required {
			name := strings.ToLower(label)
			if f := strings.Fields(label); len(f) > 0 {
				name = strings.ToLower(f[0])
			}
			return "", fmt.Errorf("boot: --%s required when stdin is not a terminal", name)
		}
		return def, nil
	}
	prompt := label
	if def != "" {
		prompt += " [" + def + "]"
	}
	fmt.Printf("  %s: ", prompt)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}
