package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/patchwidgets"
)

// newWidgetCommand groups the interactive prompt widgets under a single
// parent so the top-level CLI surface stays small.
func newWidgetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "widget",
		Short: "Interactive prompt widgets (ask, pick, confirm)",
		Long: `JSON-in / JSON-out interactive widgets for hooks and scripts.

Each subcommand reads a spec on stdin, paints itself on /dev/tty, and
writes the Result on stdout. Exit codes: 0 ok, 1 confirm-no, 2 timeout,
130 cancel/eof.`,
	}
	cmd.AddCommand(newAskCommand(), newPickCommand(), newConfirmCommand())
	return cmd
}

// newAskCommand reads an AskSpec JSON from stdin, runs the widget, writes
// the Result JSON to stdout, and exits with the conventional code.
func newAskCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ask",
		Short: "Interactive single-line text prompt (JSON in, JSON out)",
		Long: `Read an AskSpec as JSON on stdin, render an inline prompt, write
the Result as JSON on stdout. Designed for hooks and scripts.

Spec fields: input (required), default, placeholder, timeout (seconds).
Exit codes: 0 ok, 2 timeout, 130 cancel/eof.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var spec patchwidgets.AskSpec
			if err := decodeSpec(cmd.InOrStdin(), &spec); err != nil {
				return err
			}
			res, err := patchwidgets.Ask(spec)
			if err != nil {
				return err
			}
			return emitResult(cmd.OutOrStdout(), res)
		},
	}
}

// newPickCommand reads a PickSpec JSON from stdin and prints a Result
// JSON on stdout.
func newPickCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "pick",
		Short: "Interactive list selector (JSON in, JSON out)",
		Long: `Read a PickSpec as JSON on stdin, render an inline list selector,
write the Result as JSON on stdout.

Spec fields: input (required), choices (required), default, filter,
timeout. Exit codes: 0 ok, 2 timeout, 130 cancel/eof.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var spec patchwidgets.PickSpec
			if err := decodeSpec(cmd.InOrStdin(), &spec); err != nil {
				return err
			}
			res, err := patchwidgets.Pick(spec)
			if err != nil {
				return err
			}
			return emitResult(cmd.OutOrStdout(), res)
		},
	}
}

// newConfirmCommand reads a ConfirmSpec JSON from stdin and prints a
// Result JSON on stdout. Exit code 1 means the user answered "no".
func newConfirmCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "confirm",
		Short: "Interactive yes/no prompt (JSON in, JSON out)",
		Long: `Read a ConfirmSpec as JSON on stdin, render a yes/no prompt, write
the Result as JSON on stdout.

Spec fields: input (required), default ("yes"|"no"), timeout.
Exit codes: 0 yes, 1 no, 2 timeout, 130 cancel/eof.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var spec patchwidgets.ConfirmSpec
			if err := decodeSpec(cmd.InOrStdin(), &spec); err != nil {
				return err
			}
			res, err := patchwidgets.Confirm(spec)
			if err != nil {
				return err
			}
			return emitResult(cmd.OutOrStdout(), res)
		},
	}
}

// decodeSpec reads exactly one JSON object from r into v. Trailing data
// after the first object is ignored — useful when callers pipe the spec
// followed by a newline.
func decodeSpec(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("empty stdin: expected JSON spec")
		}
		return fmt.Errorf("invalid spec JSON: %w", err)
	}
	return nil
}

// emitResult writes the result as JSON + newline to w and exits the
// process with the conventional code. We exit here (rather than via
// cobra's RunE error path) so the JSON line lands on stdout cleanly
// before exit, with a precise non-zero code.
func emitResult(w io.Writer, res patchwidgets.Result) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(res); err != nil {
		return err
	}
	os.Exit(res.ExitCode())
	return nil
}
