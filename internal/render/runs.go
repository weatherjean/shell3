package render

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// RunsPage renders one page of stored sessions newest first as a compact
// inline listing whose entries are tappable /run_N commands (kept out of any
// document: Telegram only linkifies commands in message text), plus the FULL
// listing's N→id map (1 = newest) for the caller to resolve taps against.
// page is 1-based; a page past the end returns md == "" with totalPages set.
// root is the project dir holding runs/ (the value of Parts.RunsRoot()).
func RunsPage(root string, page, size int) (string, map[int]string, int, error) {
	if page < 1 || size < 1 {
		return "", nil, 0, fmt.Errorf("render: invalid page %d (size %d)", page, size)
	}
	st, err := runs.Open(root)
	if err != nil {
		return "", nil, 0, err
	}
	metas, err := st.ListSessions(0)
	if err != nil {
		return "", nil, 0, err
	}
	index := make(map[int]string, len(metas))
	for i, m := range metas {
		index[i+1] = m.ID
	}
	if len(metas) == 0 {
		return "_No runs stored._", index, 1, nil
	}
	totalPages := (len(metas) + size - 1) / size
	if page > totalPages {
		return "", index, totalPages, nil
	}
	lo := (page - 1) * size
	hi := min(lo+size, len(metas))
	var b strings.Builder
	for i, m := range metas[lo:hi] {
		fmt.Fprintf(&b, "/run_%d · %s · %s", lo+i+1, stamp(m.StartedAt), m.Status)
		if msgs, err := st.LoadMessages(m.ID); err == nil {
			if p := firstPrompt(msgs); p != "" {
				b.WriteString(" · " + oneLine(p, 60))
			}
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "\npage %d/%d", page, totalPages)
	if page < totalPages {
		fmt.Fprintf(&b, " — /runs %d for older", page+1)
	}
	return b.String(), index, totalPages, nil
}

// RunReplay renders one stored session at full fidelity: user prompts,
// reasoning, tool calls with their arguments, tool results, assistant text.
func RunReplay(root, id string) (string, error) {
	// filepath.Base leaves "." and ".." unchanged, so the equality check alone
	// admits both — and Stat then succeeds on the runs root itself.
	if id == "" || id == "." || id == ".." || id != filepath.Base(id) {
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
