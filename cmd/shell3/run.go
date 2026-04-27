package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

type runFlags struct {
	persona  string
	provider string
	model    string
	noBash   bool
	noMemory bool
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd.Context(), f, strings.Join(args, " "))
		},
	}
	cmd.Flags().StringVar(&f.persona, "persona", "base", "Persona to load from .shell3/personas/")
	cmd.Flags().StringVar(&f.provider, "provider", "", "Configured instance name or single-instance adapter (e.g. codex). Run 'shell3 auth list' to see options.")
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools")
	return cmd
}

func runChat(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}

	personasDir := filepath.Join(cwd, ".shell3/personas")
	personaName := f.persona

	pCfg, err := persona.ParseConfig(personasDir, personaName)
	if err != nil {
		return err
	}
	if err := persona.Validate(pCfg, personaName); err != nil {
		return err
	}

	if err := config.Migrate(homeDir); err != nil {
		return fmt.Errorf("migrate credentials: %w", err)
	}
	credStore, err := config.LoadCredStore(homeDir)
	if err != nil {
		return err
	}

	providerHint := coalesce(f.provider, pCfg.Provider)
	adapterName, instance, model := resolveConnection(providerHint, pCfg.Model, credStore, f)
	if adapterName == "" {
		return fmt.Errorf("no adapter configured — run: shell3 auth")
	}
	prov, ok := llm.Get(adapterName)
	if !ok {
		return fmt.Errorf("unknown adapter %q (registered: %v)", adapterName, llm.Registered())
	}

	noBash := pCfg.NoBash || f.noBash
	noMemory := pCfg.NoMemory || f.noMemory

	var st *store.Store
	storeDBPath := filepath.Join(cwd, coalesce(pCfg.DB, ".shell3/shell3.db"))
	if !noMemory {
		if s, err := store.Open(storeDBPath); err == nil {
			st = s
			defer st.Close()
		}
	}

	loadedSkills, _ := skills.LoadAll([]string{filepath.Join(cwd, ".shell3/skills")})

	envPath := filepath.Join(cwd, ".shell3", ".env")
	dotEnv, dotEnvErr := usertools.LoadDotEnv(envPath)
	if dotEnvErr != nil {
		fmt.Fprintln(os.Stderr, "warning:", dotEnvErr)
	}
	secrets := map[string]string{}
	for k, v := range dotEnv {
		secrets[k] = v
	}
	for _, kv := range os.Environ() {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		secrets[kv[:eq]] = kv[eq+1:]
	}
	available := map[string]struct{}{}
	for k := range secrets {
		available[k] = struct{}{}
	}

	toolsDirs := []string{
		filepath.Join(homeDir, ".shell3", "tools"),
		filepath.Join(cwd, ".shell3", "tools"),
	}
	loadedTools, toolWarnings, _ := usertools.LoadAll(toolsDirs, available)
	for _, w := range toolWarnings {
		fmt.Fprintln(os.Stderr, "user-tool warning:", w)
	}
	userToolDefs := make([]llm.ToolDefinition, 0, len(loadedTools))
	userToolMap := make(map[string]usertools.Tool, len(loadedTools))
	for _, ut := range loadedTools {
		userToolDefs = append(userToolDefs, llm.ToolDefinition{
			Name:        ut.Name,
			Description: ut.Description,
			Parameters:  ut.Parameters,
		})
		userToolMap[ut.Name] = ut
	}

	var coreMemories []store.MemoryEntry
	if st != nil {
		mems, err := st.MemoryQuery("", true, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: load core memories:", err)
		} else {
			coreMemories = mems
			var bytes int
			for _, m := range mems {
				bytes += len(m.Key) + len(m.Value) + 4
			}
			if bytes > 2048 {
				fmt.Fprintf(os.Stderr,
					"warning: core memories total %d bytes (>2KB), consider demoting some\n",
					bytes)
			}
		}
	}

	personaData := persona.TemplateData{
		Skills:       skills.BuildSection(loadedSkills),
		Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:          cwd,
		Model:        model,
		CoreMemories: coreMemories,
	}
	pers, err := persona.Load(personasDir, personaName, personaData, st != nil, noBash, userToolDefs)
	if err != nil {
		return err
	}

	hookRunner := hooks.NewRunner(pCfg.Config)

	statusLine := fmt.Sprintf("%s │ %s", instance, model)

	// Aggregate models across every configured instance + single-instance
	// adapters that have no row in the store yet.
	var models []chat.ModelChoice
	for _, meta := range credStore.List() {
		p, ok := llm.Get(meta.Adapter)
		if !ok {
			continue
		}
		for _, m := range p.Models(credStore, meta.Instance) {
			models = append(models, chat.ModelChoice{Provider: meta.Instance, Model: m})
		}
	}
	regNames := llm.Registered()
	sort.Strings(regNames)
	for _, name := range regNames {
		p, _ := llm.Get(name)
		if !p.SingleInstance() {
			continue
		}
		if _, _, ok := credStore.Get(name); ok {
			continue
		}
		for _, m := range p.Models(credStore, name) {
			models = append(models, chat.ModelChoice{Provider: name, Model: m})
		}
	}
	if len(models) == 0 {
		models = []chat.ModelChoice{{Provider: instance, Model: model}}
	}

	buildClient := func(inst, m string) (chat.LLMClient, error) {
		adapter := ""
		if a, _, ok := credStore.Get(inst); ok {
			adapter = a
		} else if _, ok := llm.Get(inst); ok {
			adapter = inst
		}
		if adapter == "" {
			return nil, fmt.Errorf("unknown adapter for instance %q", inst)
		}
		p, ok := llm.Get(adapter)
		if !ok {
			return nil, fmt.Errorf("unknown adapter %q", adapter)
		}
		return p.NewClient(ctx, credStore, inst, m)
	}

	streamer, err := prov.NewClient(ctx, credStore, instance, model)
	if err != nil {
		return err
	}
	var client chat.LLMClient = streamer

	modelSwitcher := func(newInstance, newModel string) (chat.LLMClient, error) {
		if newInstance == "" || newInstance == instance {
			if ms, ok := client.(llm.ModelSetter); ok {
				ms.SetModel(newModel)
				model = newModel
				return nil, nil
			}
		}
		next, err := buildClient(newInstance, newModel)
		if err != nil {
			return nil, err
		}
		client = next
		instance = newInstance
		model = newModel
		return next, nil
	}
	cfg := chat.Config{
		LLM:           client,
		Hooks:         hookRunner,
		Store:         st,
		Personality:   pers,
		WorkDir:       cwd,
		StatusLine:    statusLine,
		ModeLabel:     pCfg.Name,
		Models:        models,
		ModelSwitcher: modelSwitcher,
		Docs:          docsContent,
		UserTools:     userToolMap,
		Secrets:       secrets,
	}

	if initialInput != "" {
		return chat.RunOnce(ctx, cfg, initialInput)
	}
	return chat.RunInteractive(ctx, cfg)
}

// resolveConnection picks the (adapter, instance, model) tuple for this run.
//
// Resolution order:
//  1. providerHint (from --provider flag or persona.Provider): may name an
//     existing instance OR a single-instance adapter (e.g. "codex").
//  2. Fall back to the alphabetically first configured instance.
func resolveConnection(providerHint, modelHint string, credStore *config.CredStore, f *runFlags) (adapter, instance, model string) {
	if providerHint != "" {
		if a, _, ok := credStore.Get(providerHint); ok {
			adapter = a
			instance = providerHint
		} else if _, ok := llm.Get(providerHint); ok {
			adapter = providerHint
			instance = providerHint
		}
	}

	if adapter == "" {
		list := credStore.List()
		if len(list) > 0 {
			adapter = list[0].Adapter
			instance = list[0].Instance
		}
	}

	model = coalesce(f.model, modelHint)
	if model == "" && adapter != "" {
		if p, ok := llm.Get(adapter); ok {
			ms := p.Models(credStore, instance)
			if len(ms) > 0 {
				model = ms[0]
			}
		}
	}
	if model == "" {
		model = "llama3.2"
	}
	return
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
