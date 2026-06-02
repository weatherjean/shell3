package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/web"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
)

type webFlags struct {
	configPath string
	host       string
	port       int
}

func newWebCommand() *cobra.Command {
	f := &webFlags{}
	cmd := &cobra.Command{
		Use:   "web",
		Short: "Serve an interactive web UI for one long-lived agent session",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWeb(cmd.Context(), f)
		},
	}
	cmd.Flags().StringVarP(&f.configPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
	cmd.Flags().StringVar(&f.host, "host", "127.0.0.1", "Host/interface to bind (use 0.0.0.0 to expose; no auth — front with a reverse proxy)")
	cmd.Flags().IntVar(&f.port, "port", 8080, "Port to listen on")
	return cmd
}

func runWeb(ctx context.Context, f *webFlags) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}
	configPath, err := resolveConfigPath(f.configPath, cwd, homeDir)
	if err != nil {
		return err
	}

	g := paths.NewGlobal(homeDir)
	log, logCloser := openAppLog(g.LogFile)
	defer logCloser.Close()

	cfg, cleanup, err := buildChatConfig(configPath, cwd, homeDir, "", false, log)
	if err != nil {
		return err
	}
	defer cleanup()

	// Build one long-lived session, mirroring tui.RunInteractive's setup.
	var storeID int64
	if cfg.Store != nil {
		if id, err := cfg.Store.StartSession(); err == nil {
			storeID = id
		} else {
			log.Warn("start store session failed", "error", err)
		}
	}
	sess := chat.NewSession(chat.SessionOpts{
		BufSize:          256,
		StoreID:          storeID,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
	})
	sess.Start(map[string]string{"mode": "web"})

	handlers := chat.NewHandlers(cfg)
	tc := chat.TurnConfig{
		LLM:             cfg.LLM,
		Personality:     cfg.Personality,
		StatusLine:      cfg.StatusLine,
		WorkDir:         cfg.WorkDir,
		Store:           cfg.Store,
		Truncate:        cfg.Truncate,
		Handlers:        handlers,
		Log:             chat.LogOrNoop(cfg.Log),
		Headless:        cfg.Headless,
		CustomTool:      cfg.CustomTool,
		CustomToolNames: cfg.CustomToolNames,
		ToolGuard:       cfg.ToolGuard,
		// ShellInteractive intentionally nil: no TTY in web mode; the
		// shell_interactive tool returns an "unavailable" string.
	}

	// cfg.StatusLine is "<agent> │ <modelID>"; split out the model for the UI.
	model := cfg.StatusLine
	if i := strings.Index(cfg.StatusLine, "│"); i >= 0 {
		model = strings.TrimSpace(cfg.StatusLine[i+len("│"):])
	}

	// Active model is swappable at runtime via /model. Only one turn runs at a
	// time (the server rejects switches while busy), so a mutex guarding the
	// client swap and the per-turn snapshot is sufficient.
	var modelMu sync.Mutex
	currentModel := model
	switchModel := func(name string) (string, error) {
		am, err := cfg.SwitchModel(name)
		if err != nil {
			return "", err
		}
		modelMu.Lock()
		tc.LLM = am.Client
		currentModel = am.ModelID
		modelMu.Unlock()
		return am.ModelID, nil
	}

	hub := web.NewHub(sess, func(turnCtx context.Context, msg llm.Message) {
		modelMu.Lock()
		snapshot := tc
		modelMu.Unlock()
		if len(msg.ContentParts) == 0 {
			sess.Run(turnCtx, snapshot, msg.Content)
		} else {
			chat.RunTurn(turnCtx, snapshot, sess, msg)
		}
	})
	hub.Start()

	modelNames := make([]string, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		modelNames = append(modelNames, m.Name)
	}
	info := web.Info{
		Persona: cfg.ModeLabel,
		Project: cfg.ProjectRef,
		Prompt:  cfg.Personality.SystemPrompt,
		Tools:   cfg.ActiveTools,
		Models:  modelNames,
		Model:   func() string { modelMu.Lock(); defer modelMu.Unlock(); return currentModel },
	}
	if cfg.SwitchModel != nil && len(cfg.Models) > 1 {
		info.Switch = switchModel
	}

	srv := &http.Server{
		Addr:    net.JoinHostPort(f.host, fmt.Sprintf("%d", f.port)),
		Handler: web.NewServer(hub, info).Handler(),
	}

	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutCtx)
	}()

	fmt.Fprintf(os.Stderr, "shell3 web listening on http://%s\n", srv.Addr)
	err = srv.ListenAndServe()

	// Tear down: stop any in-flight turn and wait for its goroutine, THEN close
	// the session so the hub's drain goroutine exits cleanly.
	hub.Close()
	// Close the store session row (mirrors the TUI). Read sess.ID() now —
	// compact_history can roll the id mid-conversation, and hub.Close() has
	// already ensured no turn is in flight.
	if cfg.Store != nil {
		if err := cfg.Store.EndSession(sess.ID()); err != nil {
			log.Warn("end session failed", "session_id", sess.ID(), "error", err)
		}
	}
	sess.End("ok")
	sess.CloseEvents()

	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
