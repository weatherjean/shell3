//go:build unix

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/telegram"
	"github.com/weatherjean/shell3/internal/telegram/web"
	"github.com/weatherjean/shell3/pkg/shell3"
)

func newTelegramCommand() *cobra.Command {
	var configPath string
	cmd := &cobra.Command{
		Use:   "telegram",
		Short: "Run the personal Telegram bot front-end",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			cwd, _ := os.Getwd()
			rt, err := shell3.NewRuntime(shell3.RuntimeSpec{ConfigPath: configPath, WorkDir: cwd})
			if err != nil {
				return err
			}
			defer rt.Close()

			tg := rt.Telegram()
			if tg.Token == "" || tg.ChatID == "" {
				return fmt.Errorf("no telegram config: add shell3.telegram{ token=..., chat_id=... } to shell3.lua")
			}
			chatID, err := strconv.ParseInt(tg.ChatID, 10, 64)
			if err != nil {
				return fmt.Errorf("telegram chat_id %q is not a number: %w", tg.ChatID, err)
			}

			sess, err := rt.Session(shell3.SessionOpts{Name: "telegram"})
			if err != nil {
				return err
			}

			client, err := telegram.NewBotAPIClient(ctx, tg.Token)
			if err != nil {
				return err
			}
			b := telegram.NewBot(client, rt, sess, chatID, tg.Dashboard.URL)

			if tg.Dashboard.Enabled && tg.Dashboard.Addr != "" {
				srv := web.NewServer(rt, sess, tg.Token, chatID)
				go func() {
					_ = startDashboard(ctx, tg.Dashboard.Addr, srv.Handler())
				}()
				fmt.Printf("dashboard on %s (expose via: tailscale serve https / proxy %s)\n", tg.Dashboard.Addr, tg.Dashboard.Addr)
			}

			// If the dashboard has a public URL, set the bot's in-chat menu
			// button to open it as a Mini App (the bottom-left "Open App"
			// button). Best-effort: a failure here must not stop the bot.
			if tg.Dashboard.Enabled && tg.Dashboard.URL != "" {
				if err := client.SetMenuButton(ctx, "📊 Dashboard", tg.Dashboard.URL); err != nil {
					fmt.Printf("warning: could not set menu button: %v\n", err)
				}
			}

			fmt.Printf("shell3 telegram: listening for chat %d\n", chatID)
			b.Run(ctx)
			return nil
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "", "path to shell3.lua")
	return cmd
}

// startDashboard runs an HTTP server on addr with the given handler, and
// gracefully shuts it down when ctx is cancelled.
func startDashboard(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h}
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return <-errCh
	}
}
