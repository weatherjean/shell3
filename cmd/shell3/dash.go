//go:build unix

package main

import (
	"fmt"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram/web"
)

// newDashCommand builds `shell3 dash` — serve the Mini App dashboard locally
// with NO authentication, for development and troubleshooting. It builds a
// runtime from the same config the bot uses and reattaches to the latest
// session, so every dashboard endpoint (status, runs, jobs, cron, files,
// history) is browsable/curlable without Telegram initData. Bound to localhost
// only: with auth off it exposes history + files, so it must never face the
// network.
func newDashCommand() *cobra.Command {
	var configPath, addr string
	cmd := &cobra.Command{
		Use:   "dash",
		Short: "Serve the dashboard locally with NO auth (dev/troubleshooting)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			resolved, err := resolveConfig(configPath)
			if err != nil {
				return err
			}
			rt, err := shell3.NewRuntime(ctx, shell3.RuntimeSpec{ConfigPath: resolved, WorkDir: filepath.Dir(resolved)})
			if err != nil {
				return err
			}
			defer rt.Close()

			tg := rt.Telegram()
			var chatID int64
			if tg.ChatID != "" {
				if chatID, err = strconv.ParseInt(tg.ChatID, 10, 64); err != nil {
					return fmt.Errorf("telegram chat_id %q is not a number: %w", tg.ChatID, err)
				}
			}
			// Reattach to the latest session so the dashboard shows the bot's real
			// history + runs (subagent child sessions included).
			sess, err := rt.Session(shell3.SessionOpts{Name: "dash", WorkDir: tg.WorkDir, ResumeLatest: true})
			if err != nil {
				return err
			}

			srv := web.NewServer(rt, sess, tg.Token, chatID)
			srv.SetDevNoAuth()
			srv.SetConfigDir(filepath.Dir(resolved))

			if addr == "" {
				addr = tg.Dashboard.Addr
			}
			if addr == "" {
				addr = "127.0.0.1:8765"
			}
			fmt.Printf("shell3 dash: serving the dashboard on http://%s  (NO AUTH — localhost only)\n", addr)
			fmt.Printf("  config: %s\n  API:    /api/{status,sessions,session,jobs,job,history,cron,files,file}\n", resolved)
			return startDashboard(ctx, addr, srv.Handler())
		},
	}
	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Config name (→ ~/.shell3/<name>.lua) or path to a *.lua file")
	cmd.Flags().StringVar(&addr, "addr", "", "Listen address (default: the config's dashboard addr, else 127.0.0.1:8765)")
	return cmd
}
