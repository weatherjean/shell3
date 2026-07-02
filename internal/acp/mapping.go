// Package acp implements the ACP (Agent Client Protocol) front-end for shell3.
// This file contains pure, stateless protocol mapper functions with no I/O.
package acp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// toolKind maps a shell3 tool name to the corresponding ACP ToolKind.
func toolKind(name string) acpsdk.ToolKind {
	switch name {
	case "bash", "bash_bg", "shell_interactive":
		return acpsdk.ToolKindExecute
	case "read":
		return acpsdk.ToolKindRead
	case "list_files":
		return acpsdk.ToolKindSearch
	case "edit_file":
		return acpsdk.ToolKindEdit
	default:
		return acpsdk.ToolKindOther
	}
}

// promptToParts converts a slice of ACP ContentBlocks into a flat prompt string
// and a slice of shell3 media Parts.
//
// Text blocks and text-like blocks (resource_link, embedded text resource) are
// joined with newlines into the prompt string. Image and audio blocks are
// base64-decoded into shell3.Part values. Bad base64 data returns an error.
func promptToParts(blocks []acpsdk.ContentBlock) (string, []shell3.Part, error) {
	var textSegments []string
	var parts []shell3.Part

	for _, block := range blocks {
		switch {
		case block.Text != nil:
			textSegments = append(textSegments, block.Text.Text)

		case block.ResourceLink != nil:
			textSegments = append(textSegments, block.ResourceLink.Uri)

		case block.Resource != nil:
			res := block.Resource.Resource
			if res.TextResourceContents != nil {
				fenced := "```\n" + res.TextResourceContents.Text + "\n```"
				textSegments = append(textSegments, fenced)
			}

		case block.Image != nil:
			decoded, err := base64.StdEncoding.DecodeString(block.Image.Data)
			if err != nil {
				return "", nil, fmt.Errorf("image base64 decode: %w", err)
			}
			parts = append(parts, shell3.Part{
				Kind: shell3.PartImage,
				Data: decoded,
				MIME: block.Image.MimeType,
			})

		case block.Audio != nil:
			decoded, err := base64.StdEncoding.DecodeString(block.Audio.Data)
			if err != nil {
				return "", nil, fmt.Errorf("audio base64 decode: %w", err)
			}
			parts = append(parts, shell3.Part{
				Kind: shell3.PartAudio,
				Data: decoded,
				MIME: block.Audio.MimeType,
			})
		}
	}

	return strings.Join(textSegments, "\n"), parts, nil
}

// updatesForEvent converts a single shell3.Event into zero or more
// acp.SessionUpdate values for streaming to an ACP client.
//
// Mapping:
//
//	Token          → UpdateAgentMessageText
//	Reasoning      → UpdateAgentThoughtText
//	SystemReminder → UpdateAgentThoughtText (host aside, marked with ⚠)
//	ToolCall       → StartToolCall (InProgress; title = bash command or tool name)
//	ToolResult     → UpdateToolCall (Completed or Failed based on ToolError)
//	everything else → nil (handled by callers: session.go, etc.)
func updatesForEvent(ev shell3.Event) []acpsdk.SessionUpdate {
	switch ev.Kind {
	case shell3.Token:
		return []acpsdk.SessionUpdate{acpsdk.UpdateAgentMessageText(ev.Text)}

	case shell3.Reasoning:
		return []acpsdk.SessionUpdate{acpsdk.UpdateAgentThoughtText(ev.Text)}

	case shell3.SystemReminder:
		// Host-injected <system-reminder> blocks (model change, context-usage
		// threshold, queued-input notices) are meta-context, not agent speech, so
		// they surface as a thought chunk — clients render thoughts distinctly
		// (often dimmed/collapsible). A warning glyph marks it as a host aside.
		return []acpsdk.SessionUpdate{acpsdk.UpdateAgentThoughtText("⚠ " + ev.Text)}

	case shell3.ToolCall:
		title := toolCallTitle(ev.ToolName, ev.ToolInput)
		u := acpsdk.StartToolCall(
			acpsdk.ToolCallId(ev.ToolCallID),
			title,
			acpsdk.WithStartKind(toolKind(ev.ToolName)),
			acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
			acpsdk.WithStartRawInput(json.RawMessage(ev.ToolInput)),
		)
		return []acpsdk.SessionUpdate{u}

	case shell3.ToolResult:
		status := acpsdk.ToolCallStatusCompleted
		if ev.ToolError {
			status = acpsdk.ToolCallStatusFailed
		}
		u := acpsdk.UpdateToolCall(
			acpsdk.ToolCallId(ev.ToolCallID),
			acpsdk.WithUpdateStatus(status),
			acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
				acpsdk.ToolContent(acpsdk.TextBlock(ev.ToolOutput)),
			}),
			acpsdk.WithUpdateRawOutput(ev.ToolOutput),
		)
		return []acpsdk.SessionUpdate{u}

	default:
		// Compacted, Usage, Retry, Error, Done — handled elsewhere.
		return nil
	}
}

// toolCallTitle returns the display title for a tool call.
// For bash-family tools, it extracts the "command" field from the ToolInput JSON.
// For all other tools it returns the tool name.
func toolCallTitle(toolName, toolInput string) string {
	switch toolName {
	case "bash", "bash_bg", "shell_interactive":
		var v struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(toolInput), &v); err == nil && v.Command != "" {
			return v.Command
		}
	}
	return toolName
}
