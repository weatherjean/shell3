package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/scaffold"
)

type bootFlags struct {
	url, model, name, key, proxy, braveKey string
	force                                  bool
}

func newBootCommand() *cobra.Command {
	f := &bootFlags{}
	cmd := &cobra.Command{
		Use:   "boot",
		Short: "Create a shell3 config interactively (url, model, name, key)",
		RunE:  func(cmd *cobra.Command, args []string) error { return runBoot(f) },
	}
	cmd.Flags().StringVar(&f.url, "url", "", "Base URL (OpenAI-compatible endpoint)")
	cmd.Flags().StringVar(&f.model, "model", "", "Model tag/id")
	cmd.Flags().StringVar(&f.name, "name", "", "Handle for this model (default: main)")
	cmd.Flags().StringVar(&f.key, "key", "", "API key")
	cmd.Flags().StringVar(&f.proxy, "proxy", "", "Optional run_proxy command")
	cmd.Flags().StringVar(&f.braveKey, "brave-key", "", "Optional Brave Search API key")
	cmd.Flags().BoolVar(&f.force, "force", false, "Overwrite an existing ~/.shell3/shell3.lua")
	return cmd
}

func runBoot(f *bootFlags) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("boot: home dir: %w", err)
	}
	dir := filepath.Join(home, ".shell3")
	cfgPath := filepath.Join(dir, "shell3.lua")

	if _, err := os.Stat(cfgPath); err == nil && !f.force {
		return fmt.Errorf("boot: %s already exists — pass --force to overwrite", cfgPath)
	}

	in := bufio.NewReader(os.Stdin)
	tty := term.IsTerminal(int(os.Stdin.Fd()))

	url, err := value(f.url, "Base URL", "https://api.openai.com/v1", in, tty, false)
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
	key, err := secret(f.key, "API key", in, tty, true)
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
	braveKey, err := secret(f.braveKey, "Brave Search key (blank to add later)", in, tty, false)
	if err != nil {
		return err
	}

	envKey := envKeyForName(name)

	if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
		Name: name, BaseURL: url, EnvKey: envKey, Model: model, Proxy: proxy,
	}, f.force); err != nil {
		return err
	}

	envPath := filepath.Join(dir, ".env")
	existing, err := os.ReadFile(envPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("boot: read .env: %w", err)
	}
	merged := mergeEnv(string(existing), [][2]string{
		{envKey, key},
		{"BRAVE_API_KEY", braveKey},
	})
	if err := os.WriteFile(envPath, []byte(merged), 0600); err != nil {
		return fmt.Errorf("boot: write .env: %w", err)
	}

	printBootSuccess(dir, cfgPath, envPath, proxy != "")
	return nil
}

// envKeyForName derives the .env key for a model handle: uppercased, with any
// non-alphanumeric run collapsed to a single underscore, suffixed _API_KEY. It
// guarantees a valid identifier: an empty result falls back to MAIN, and a
// leading digit is prefixed with an underscore.
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

// mergeEnv appends each key=value from kv to existing only if the key is not
// already present as its own line. Existing values are never changed. The
// result always ends with a trailing newline.
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
	fmt.Println("Edit shell3.lua (and lib/) to add tools, skills, MCP, or agents —")
	fmt.Println("recipes live in the shell3 repo under docs/cookbook/.")
	fmt.Println()
	fmt.Println(`Run:  shell3 "hello"`)
}

// value reads a config value: flag wins; else prompt (TTY) with optional
// default; errors when required and unavailable.
func value(flag, label, def string, in *bufio.Reader, tty, required bool) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !tty {
		if required {
			return "", fmt.Errorf("boot: --%s required when stdin is not a terminal", strings.ToLower(strings.Fields(label)[0]))
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

// secret reads a value without echoing it.
func secret(flag, label string, in *bufio.Reader, tty, required bool) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if !tty {
		if required {
			return "", fmt.Errorf("boot: --%s required when stdin is not a terminal", strings.ToLower(strings.Fields(label)[0]))
		}
		return "", nil
	}
	fmt.Printf("  %s: ", label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
