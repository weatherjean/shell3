//go:build unix

package main

import "github.com/spf13/cobra"

// addConfigAgentFlags registers the --config/--agent pair every front-end
// command takes, with uniform wording. agentDesc overrides the --agent help
// text when non-empty (run's variant also accepts subagent names).
func addConfigAgentFlags(cmd *cobra.Command, config, agent *string, agentDesc string) {
	cmd.Flags().StringVarP(config, "config", "c", "", "Config name (→ ~/.shell3/<name>.lua) or path to a *.lua file (default: ~/.shell3/shell3.lua)")
	if agentDesc == "" {
		agentDesc = "Select the active agent by name (default: first declared)"
	}
	cmd.Flags().StringVar(agent, "agent", "", agentDesc)
}

// addResumeFlag registers --resume with the wording shared by the root and
// run commands (both resolve the session's recorded config the same way).
func addResumeFlag(cmd *cobra.Command, resume *string) {
	cmd.Flags().StringVar(resume, "resume", "", "Resume a stored session by id: reload its messages and continue the conversation")
}
