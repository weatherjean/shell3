//go:build unix

package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/telegram"
)

// newServeCommand builds `shell3 serve` — the BYO front-end seam. It runs the
// exact same bot loop as `shell3 telegram` (fresh-turn threading, host
// commands, hook asks, notifier completion delivery, cron), but the transport
// is newline-delimited JSON over stdin/stdout: a Discord bridge, a custom
// dashboard backend, or a test harness spawns this process and translates.
// docs/serve.md is the wire reference.
//
// No Telegram credentials are required — possessing the process's stdio IS
// the access model, the exact parallel of chat_id. There is no port and no
// listener.
func newServeCommand() *cobra.Command {
	var configDir string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the agent over stdio JSONL for a bring-your-own front-end",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			rt, resolved, err := openRuntime(ctx, configDir)
			if err != nil {
				return err
			}
			defer rt.Close()

			// The agent's shell runs in telegram.workdir when set (the block is
			// wiring shared by both front-ends); the config dir is the fallback.
			// Token and chat_id are NOT read — an absent telegram: block is fine.
			workDir := rt.Telegram().WorkDir
			if workDir == "" {
				workDir = resolved
			}

			// Serve keeps its own thread index beside telegram's: the id spaces
			// are different transports' and must not cross-resolve.
			threads, err := openThreads(rt, "serve_threads.jsonl")
			if err != nil {
				return err
			}

			jc := telegram.NewJSONLClient(os.Stdin, os.Stdout, telegram.ConsoleChatID,
				filepath.Join(rt.Parts().RunsRoot(), "serve_out"))
			jc.Logf = func(format string, a ...any) {
				fmt.Fprintf(os.Stderr, format+"\n", a...)
			}
			b := telegram.NewBot(jc, rt, telegram.ConsoleChatID, threads)

			stopSched, err := wireHost(b, rt, workDir)
			if err != nil {
				return err
			}
			defer stopSched()

			// Handshake first (protocol version + the host-answered command
			// menu), then the greeting as an ordinary send event.
			jc.EmitHello(telegram.BotCommands())
			if _, err := jc.Send(ctx, telegram.ConsoleChatID,
				"๑ï shell3 online — send a message event to start a thread; reply_to_id continues one."); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not send the greeting: %v\n", err)
			}
			fmt.Fprintf(os.Stderr, "shell3 serve: JSONL on stdio\n  config: %s\n", resolved)

			b.Run(ctx)
			return nil
		},
	}
	addConfigFlag(cmd, &configDir)
	return cmd
}
