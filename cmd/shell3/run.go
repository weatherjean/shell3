package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/pkg/hooks"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/pkg/persona"
	"github.com/weatherjean/shell3/internal/scaffold"
	"github.com/weatherjean/shell3/internal/secrets"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/internal/usertools"
)

type runFlags struct {
	persona  string
	provider string
	model    string
	noBash   bool
	noMemory bool
	outPath  string
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			input := strings.TrimSpace(strings.Join(args, " "))
			if input == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				input = strings.TrimSpace(string(b))
			}
			return runChat(cmd.Context(), f, input)
		},
	}
	cmd.Flags().StringVar(&f.persona, "persona", scaffold.DefaultPersonaName, "Persona name — loads from .shell3/personas/ or ~/.shell3/personas/")
	cmd.Flags().StringVar(&f.provider, "provider", "", "Instance name from ~/.shell3/ai-do-not-read.auth.yaml (default: first configured instance)")
	cmd.Flags().StringVar(&f.model, "model", "", "Model ID override (default: first model in the instance's models list)")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash and shell_interactive tools")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools; skip opening the store")
	cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>. Enables headless mode.")
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

	headless := f.outPath != "" || (!term.IsTerminal(int(os.Stdin.Fd())) && initialInput != "")
	if headless {
		_ = os.Setenv("SHELL3_HEADLESS", "1")
		if f.outPath != "" {
			_ = os.Setenv("SHELL3_OUT", f.outPath)
		}
	}

	g := paths.NewGlobal(homeDir)
	l := paths.NewLocal(cwd)

	if err := bootstrap.EnsureGlobal(g); err != nil {
		return err
	}
	uuid, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		return err
	}

	const logMaxBytes = 2 * 1024 * 1024 // 2 MB per log file
	const logArchives = 3               // keep .1 .2 .3 → max ~8 MB total
	log, logCloser, err := applog.Open(g.LogFile, logMaxBytes, logArchives)
	if err != nil {
		// Non-fatal: fall back to Noop so the rest of startup continues.
		fmt.Fprintln(os.Stderr, "warning: open log file:", err)
		log = applog.Noop{}
		logCloser = io.NopCloser(nil)
	}
	defer logCloser.Close()
	proj := paths.NewProject(g, uuid)

	personaName := f.persona
	pCfg, personaBody, err := persona.ParseConfig([]string{l.Personas, g.Personas}, personaName)
	if err != nil {
		return err
	}

	authStore, err := config.LoadAuthStore(homeDir)
	if err != nil {
		return err
	}

	providerHint := coalesce(f.provider, pCfg.Provider)
	instance, model := resolveConnection(providerHint, pCfg.Model, authStore, f)
	if instance == "" {
		return fmt.Errorf("no provider configured — run: shell3 auth")
	}

	noBash := pCfg.NoBash || f.noBash
	noMemory := pCfg.NoMemory || f.noMemory

	// DB path: use persona override if set, otherwise project UUID dir.
	storeDBPath := proj.DB
	if pCfg.DB != "" {
		storeDBPath = pCfg.DB
	}

	var st *store.Store
	if !noMemory {
		if s, err := store.Open(storeDBPath); err == nil {
			st = s
			defer func() { _ = st.Close() }()
		} else {
			log.Warn("open store failed — memory and history unavailable", "error", err)
		}
	}

	allSkills, err := skills.LoadAll([]string{g.Skills, l.Skills})
	if err != nil {
		log.Warn("load skills failed", "error", err)
	}
	loadedSkills := filterSkills(allSkills, pCfg.Skills)

	secStore, err := secrets.Load(homeDir)
	if err != nil {
		return fmt.Errorf("load secrets: %w", err)
	}
	secretsMap := secStore.All()
	available := map[string]struct{}{}
	for k := range secretsMap {
		available[k] = struct{}{}
	}

	toolsDirs := []string{g.Tools, l.Tools}
	allTools, toolWarnings, _ := usertools.LoadAll(toolsDirs, available)
	for _, w := range toolWarnings {
		log.Warn("user-tool warning: "+w)
	}
	loadedTools := filterTools(allTools, pCfg.Tools)
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
		mems, err := st.MemoryQuery(true, 0)
		if err != nil {
			log.Warn("load core memories failed", "error", err)
		} else {
			coreMemories = mems
			var memBytes int
			for _, m := range mems {
				memBytes += len(m.Key) + len(m.Value) + 4
			}
			if memBytes > 2048 {
				log.Warn("core memories exceed 2KB — consider demoting some", "bytes", memBytes)
			}
		}
	}

	personaData := persona.TemplateData{
		Skills:       skills.BuildSection(loadedSkills),
		Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:          cwd,
		Model:        model,
		CoreMemories: coreMemories,
		UserTools:    userToolDefs,
	}
	pers, err := persona.Load(pCfg, personaBody, personaData, st != nil, noBash, userToolDefs)
	if err != nil {
		return err
	}

	hookRunner := hooks.NewRunner(pCfg.Config)
	hookRunner.SetLogger(log)

	statusLine := fmt.Sprintf("%s │ %s", instance, model)
	if pers.Parameters.ReasoningEffort != "" {
		statusLine += " │ " + pers.Parameters.ReasoningEffort
	}

	var models []chat.ModelChoice
	for _, inst := range authStore.List() {
		for _, m := range inst.Models {
			models = append(models, chat.ModelChoice{
				Provider:      inst.Name,
				Model:         m.ID,
				ContextWindow: m.ContextWindow,
			})
		}
	}
	if len(models) == 0 {
		models = []chat.ModelChoice{{Provider: instance, Model: model}}
	}

	buildClient := func(instName, m string) (chat.LLMClient, error) {
		instCfg, ok := authStore.Get(instName)
		if !ok {
			return nil, fmt.Errorf("no instance %q in auth store", instName)
		}
		p, ok := llm.Get(instCfg.Type)
		if !ok {
			return nil, fmt.Errorf("unknown adapter type %q for instance %q — set type to \"openai\" or \"anthropic\"", instCfg.Type, instName)
		}
		return p.NewClient(ctx, authStore, instName, m)
	}

	streamer, err := buildClient(instance, model)
	if err != nil {
		return err
	}
	if setter, ok := streamer.(llm.ParamSetter); ok {
		setter.SetParams(pers.Parameters)
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
	skillNames := make([]string, 0, len(loadedSkills))
	for _, s := range loadedSkills {
		skillNames = append(skillNames, s.Name)
	}
	toolNames := make([]string, 0, len(pers.Tools))
	for _, t := range pers.Tools {
		toolNames = append(toolNames, t.Name)
	}

	cfg := chat.Config{
		LLM:           client,
		Hooks:         hookRunner,
		Store:         st,
		Personality:   pers,
		WorkDir:       cwd,
		StatusLine:    statusLine,
		ModeLabel:     pCfg.Name,
		ProjectRef:    uuid,
		ActiveSkills:  skillNames,
		ActiveTools:   toolNames,
		Models:        models,
		ModelSwitcher: modelSwitcher,
		Docs:          docsContent,
		UserTools:     userToolMap,
		Secrets:       secretsMap,
		Params:        pers.Parameters,
		Log:           log,
		OutPath:       f.outPath,
		Headless:      headless,
	}
	cfg.Reloader = func() (persona.Persona, map[string]usertools.Tool, error) {
		newPCfg, newBody, err := persona.ParseConfig([]string{l.Personas, g.Personas}, personaName)
		if err != nil {
			return persona.Persona{}, nil, err
		}
		newAllSkills, _ := skills.LoadAll([]string{g.Skills, l.Skills})
		newLoadedSkills := filterSkills(newAllSkills, newPCfg.Skills)

		newAllTools, _, _ := usertools.LoadAll(toolsDirs, available)
		newLoadedTools := filterTools(newAllTools, newPCfg.Tools)
		newUserToolDefs := make([]llm.ToolDefinition, 0, len(newLoadedTools))
		newUserToolMap := make(map[string]usertools.Tool, len(newLoadedTools))
		for _, ut := range newLoadedTools {
			newUserToolDefs = append(newUserToolDefs, llm.ToolDefinition{
				Name:        ut.Name,
				Description: ut.Description,
				Parameters:  ut.Parameters,
			})
			newUserToolMap[ut.Name] = ut
		}

		var newCoreMems []store.MemoryEntry
		if st != nil {
			newCoreMems, _ = st.MemoryQuery(true, 0)
		}

		newData := persona.TemplateData{
			Skills:       skills.BuildSection(newLoadedSkills),
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          cwd,
			Model:        model,
			CoreMemories: newCoreMems,
			UserTools:    newUserToolDefs,
		}
		newPers, err := persona.Load(newPCfg, newBody, newData, st != nil, noBash, newUserToolDefs)
		if err != nil {
			return persona.Persona{}, nil, err
		}
		return newPers, newUserToolMap, nil
	}

	if initialInput != "" {
		return tui.RunOnce(ctx, cfg, initialInput)
	}
	return tui.RunInteractive(ctx, cfg)
}

// resolveConnection picks the (instance, model) pair for this run.
func resolveConnection(providerHint, modelHint string, store *config.AuthStore, f *runFlags) (instance, model string) {
	if providerHint != "" {
		if _, ok := store.Get(providerHint); ok {
			instance = providerHint
		}
	}
	if instance == "" {
		if insts := store.List(); len(insts) > 0 {
			instance = insts[0].Name
		}
	}
	model = coalesce(f.model, modelHint)
	if model == "" && instance != "" {
		if inst, ok := store.Get(instance); ok && len(inst.Models) > 0 {
			model = inst.Models[0].ID
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

// filterSkills returns all skills when allowlist is empty, otherwise only
// those whose name appears in the allowlist.
func filterSkills(all []skills.Skill, allowlist []string) []skills.Skill {
	if len(allowlist) == 0 {
		return all
	}
	set := make(map[string]struct{}, len(allowlist))
	for _, n := range allowlist {
		set[n] = struct{}{}
	}
	var out []skills.Skill
	for _, s := range all {
		if _, ok := set[s.Name]; ok {
			out = append(out, s)
		}
	}
	return out
}

// filterTools returns all tools when allowlist is empty, otherwise only
// those whose name appears in the allowlist.
func filterTools(all []usertools.Tool, allowlist []string) []usertools.Tool {
	if len(allowlist) == 0 {
		return all
	}
	set := make(map[string]struct{}, len(allowlist))
	for _, n := range allowlist {
		set[n] = struct{}{}
	}
	var out []usertools.Tool
	for _, t := range all {
		if _, ok := set[t.Name]; ok {
			out = append(out, t)
		}
	}
	return out
}
