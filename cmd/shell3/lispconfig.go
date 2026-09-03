package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/lispconfig"
	scheduler "github.com/weatherjean/shell3/internal/schedule"
)

func newLispConfigCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate shell3.lisp configuration",
	}
	c.AddCommand(newLispConfigCheckCommand())
	c.AddCommand(newLispConfigSkillCommand())
	return c
}

func newLispConfigSkillCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "skill <shell3.lisp> <name>",
		Short: "Print one embedded skill body",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := lispconfig.Load(args[0])
			if err != nil {
				return err
			}
			for _, skill := range cfg.Skills {
				if skill.Name != args[1] {
					continue
				}
				_, err := io.WriteString(cmd.OutOrStdout(), strings.TrimSpace(skill.Instructions)+"\n")
				return err
			}
			return fmt.Errorf("%s: unknown skill %q", args[0], args[1])
		},
	}
}

func newLispConfigCheckCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "check <shell3.lisp>",
		Short: "Parse, resolve, and strictly validate shell3.lisp",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := lispconfig.Load(args[0])
			if err != nil {
				return err
			}
			if _, err := scheduler.Resolve(args[0], cfg); err != nil {
				return err
			}
			main := "none"
			if cfg.Main != nil {
				main = cfg.Main.Model
			}
			transport := "console"
			if cfg.Telegram != nil {
				transport = "console+telegram"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: ok (orchestrator=%s, transport=%s, %d model(s), %d skill(s), %d runner(s), %d agent(s), %d schedule(s))\n",
				args[0], main, transport, len(cfg.Models), len(cfg.Skills), len(cfg.Runners), len(cfg.Agents), len(cfg.Schedules))
			return nil
		},
	}
}
