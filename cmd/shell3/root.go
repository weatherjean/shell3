//go:build unix

package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/weatherjean/shell3/internal/cli"
	"github.com/weatherjean/shell3/internal/console"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/orchestrator"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/wrk"
)

// newRootCommand is shell3's local front end. The bare command starts the
// attached conversation loop; -p runs one headless turn.
func newRootCommand() *cobra.Command {
	var configPath, workDir, promptFlag string
	var here bool
	cmd := &cobra.Command{
		Use:   "shell3",
		Short: "Minimal Unix-composable agent harness",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			resolved, err := resolveRuntimePaths(cmd, configPath, workDir, here)
			if err != nil {
				return err
			}
			configPath, workDir = resolved.config, resolved.workDir
			prompt := promptFlag
			interactive := prompt == ""
			rt, err := orchestrator.Open(cmd.Context(), configPath, workDir)
			if err != nil {
				return err
			}
			defer rt.Close()
			sess, err := rt.Session(shell3.SessionOpts{Name: "chat", Headless: !interactive})
			if err != nil {
				return err
			}
			if interactive {
				mailbox := inbox.Store{Root: paths.NewLocal(workDir).Root}
				router, err := wrk.StartRouter(cmd.Context(), mailbox.Root, nil, rt.Logger())
				if err != nil {
					return err
				}
				defer router.Close()
				return console.RunWithReload(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), rt, sess, mailbox, func() error {
					_, err := orchestrator.Reload(rt, configPath, workDir, false)
					if err != nil {
						rt.Logger().Warn("config reload failed", "config", configPath, "error", err)
					}
					return err
				})
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "config=%s\n", configPath)
			if err := console.RunOne(cmd.Context(), cmd.OutOrStdout(), sess, prompt); err != nil {
				return err
			}
			return cli.WaitForBackgroundJobs(cmd.Context(), cmd.OutOrStdout(), rt, sess)
		},
	}
	cmd.AddCommand(newTelegramCommand())
	cmd.AddCommand(newLispConfigCommand())
	cmd.AddCommand(newWrkCommand())
	cmd.AddCommand(newScheduleCommand())
	cmd.AddCommand(newServiceCommand())
	cmd.AddCommand(newNotifyCommand())
	cmd.AddCommand(newInboxCommand())
	cmd.AddCommand(newBootCommand())
	addRuntimeFlags(cmd, &configPath, &workDir, &here)
	cmd.Flags().StringVarP(&promptFlag, "prompt", "p", "", "Run one headless turn instead of starting the conversation loop")
	return cmd
}
