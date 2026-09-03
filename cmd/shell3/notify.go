//go:build unix

package main

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/inbox"
)

func newNotifyCommand() *cobra.Command {
	var stateRoot, workDir, destination, source, event, correlation string
	var here bool
	c := &cobra.Command{
		Use:   "notify [message]",
		Short: "Persist an inbox message and alert the active host",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := resolveStateRoot(cmd, stateRoot, workDir, here)
			if err != nil {
				return err
			}
			body := ""
			if len(args) == 1 {
				body = args[0]
			} else {
				data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), (1<<20)+1))
				if err != nil {
					return err
				}
				if len(data) > 1<<20 {
					return errors.New("notify: message exceeds 1048576 bytes")
				}
				body = strings.TrimSuffix(string(data), "\n")
			}
			receipt, err := (inbox.Store{Root: root}).Notify(inbox.Request{
				To: destination, Source: source, Event: event, Correlation: correlation, Body: body,
			})
			if err != nil {
				return err
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetEscapeHTML(false)
			return enc.Encode(receipt)
		},
	}
	c.Flags().StringVar(&stateRoot, "state", "", "Override the shell3 state directory")
	c.Flags().StringVar(&workDir, "workdir", "", "Runtime working directory (default ~/.shell3/workdir)")
	c.Flags().BoolVar(&here, "here", false, "Use the current directory's state")
	c.Flags().StringVar(&destination, "to", "", "Destination: main or wrk:<task>/<run-id>")
	c.Flags().StringVar(&source, "source", "local-process", "Machine-origin source label")
	c.Flags().StringVar(&event, "event", "message", "Event kind")
	c.Flags().StringVar(&correlation, "correlation", "", "Optional correlation identifier")
	_ = c.MarkFlagRequired("to")
	return c
}
