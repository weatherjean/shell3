package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// RunsList renders stored sessions newest first — conversations, subagent
// children, cron runs alike. root is the project dir holding runs/ (the value
// of Parts.RunsRoot()). limit <= 0 lists everything.
func RunsList(root string, limit int) (string, error) {
	st, err := runs.Open(root)
	if err != nil {
		return "", err
	}
	metas, err := st.ListSessions(limit)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString("# Runs\n\n")
	if len(metas) == 0 {
		b.WriteString("_No runs stored._\n")
		return b.String(), nil
	}
	for _, m := range metas {
		msgs, _ := st.LoadMessages(m.ID)
		fmt.Fprintf(&b, "## `%s`\n\n", m.ID)
		field(&b, "started", stamp(m.StartedAt))
		field(&b, "last activity", stamp(m.LastAt))
		field(&b, "status", m.Status)
		field(&b, "model", m.Model)
		field(&b, "workdir", m.Workdir)
		if m.ParentID != "" {
			field(&b, "child of", m.ParentID)
		}
		field(&b, "messages", fmt.Sprintf("%d", len(msgs)))
		if p := firstPrompt(msgs); p != "" {
			field(&b, "prompt", oneLine(p, 160))
		}
		b.WriteString("\n")
	}
	return b.String(), nil
}

// RunReplay renders one stored session at full fidelity: user prompts,
// reasoning, tool calls with their arguments, tool results, assistant text.
func RunReplay(root, id string) (string, error) {
	if id == "" || id != filepath.Base(id) {
		return "", fmt.Errorf("render: invalid run id %q", id)
	}
	if info, err := os.Stat(filepath.Join(root, "runs", id)); err != nil || !info.IsDir() {
		return "", fmt.Errorf("render: no such run %q", id)
	}
	st, err := runs.Open(root)
	if err != nil {
		return "", err
	}
	msgs, err := st.LoadMessages(id)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Run `%s`\n\n", id)
	field(&b, "messages", fmt.Sprintf("%d", len(msgs)))
	b.WriteString("\n")
	if len(msgs) == 0 {
		b.WriteString("_This run has no messages._\n")
		return b.String(), nil
	}

	for i, m := range msgs {
		switch m.Role {
		case llm.RoleTool:
			name := m.Name
			if name == "" {
				name = "tool"
			}
			fmt.Fprintf(&b, "## %d · result: %s\n\n", i+1, name)
			fence(&b, "", stripToolIDPrefix(m.Content))
		default:
			fmt.Fprintf(&b, "## %d · %s\n\n", i+1, string(m.Role))
			if m.ReasoningContent != "" {
				quote(&b, m.ReasoningContent)
			}
			if text := strings.TrimSpace(m.Content); text != "" {
				b.WriteString(truncate(text) + "\n\n")
			}
			for _, part := range m.ContentParts {
				if part.Type != "" && part.Type != "text" {
					fmt.Fprintf(&b, "_(attachment: %s)_\n\n", part.Type)
				}
			}
			for _, call := range m.ToolCalls {
				fmt.Fprintf(&b, "**tool:** `%s`\n\n", call.Name)
				fence(&b, "json", call.RawArgs)
			}
		}
	}
	return b.String(), nil
}

// firstPrompt is the run's opening user message — what the run was asked to do,
// which identifies it in a listing better than its latest line does.
func firstPrompt(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

// stripToolIDPrefix drops the "[tool_call_id=…]\n" line the turn loop prepends
// to a stored tool result.
func stripToolIDPrefix(content string) string {
	if strings.HasPrefix(content, "[tool_call_id=") {
		if nl := strings.IndexByte(content, '\n'); nl >= 0 {
			return content[nl+1:]
		}
	}
	return content
}
