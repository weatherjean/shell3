package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

func dispatchUserTool(ctx context.Context, tool usertools.Tool, rawArgs string, secrets map[string]string, workDir string) string {
	out, err := usertools.Run(ctx, tool, rawArgs, secrets, workDir)
	if err != nil {
		if out != "" {
			return out + "\nerror: " + err.Error()
		}
		return "error: " + err.Error()
	}
	return out
}

const bashTimeout = 30 * time.Second

func executeBash(ctx context.Context, command, workDir string) string {
	ctx, cancel := context.WithTimeout(ctx, bashTimeout)
	defer cancel()

	c := exec.CommandContext(ctx, "bash", "-c", command)
	c.Dir = workDir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	if err := c.Run(); err != nil {
		if buf.Len() == 0 {
			fmt.Fprintf(&buf, "error: %v\n", err)
		}
	}
	if buf.Len() == 0 {
		return "(no output)"
	}
	return buf.String()
}

func dispatchStore(name, rawArgs string, st *store.Store) string {
	if st == nil {
		return fmt.Sprintf("error: store not available for tool %s", name)
	}
	var args map[string]any
	json.Unmarshal([]byte(rawArgs), &args)

	switch name {
	case "memory_store":
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		if err := st.MemoryStore(key, value); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "Stored: " + key
	case "memory_list":
		results, err := st.MemoryList(50)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if len(results) == 0 {
			return "No memories stored."
		}
		var sb strings.Builder
		for _, r := range results {
			fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
		}
		return sb.String()
	case "memory_search":
		q, _ := args["query"].(string)
		results, err := st.MemorySearch(q, 5)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if len(results) == 0 {
			return "No memories found."
		}
		var sb strings.Builder
		for _, r := range results {
			fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
		}
		return sb.String()
	case "memory_remove":
		key, _ := args["key"].(string)
		if err := st.MemoryDelete(key); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return "Removed: " + key
	case "history_latest":
		results, err := st.HistoryLatest(20)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if len(results) == 0 {
			return "No history found."
		}
		var sb strings.Builder
		for _, r := range results {
			fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
				r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
		}
		return sb.String()
	case "history_search":
		q, _ := args["query"].(string)
		results, err := st.SearchHistory(q, 5)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if len(results) == 0 {
			return "No history found."
		}
		var sb strings.Builder
		for _, r := range results {
			fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
				r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
		}
		return sb.String()
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func truncateOutput(s string) string {
	const maxLines = 10
	const maxBytes = 1000

	lines := strings.Split(s, "\n")
	// Walk lines, stopping at whichever limit hits first.
	var kept []string
	used := 0
	for i, l := range lines {
		if i >= maxLines {
			remaining := strings.Join(lines[i:], "\n")
			return strings.Join(kept, "\n") + fmt.Sprintf("\n… (+%d lines)\n", strings.Count(remaining, "\n")+1)
		}
		if used+len(l)+1 > maxBytes {
			leftover := len(s) - used
			return strings.Join(kept, "\n") + fmt.Sprintf("\n… (+%d bytes)\n", leftover)
		}
		kept = append(kept, l)
		used += len(l) + 1 // +1 for newline
	}
	return s
}

func parseBashCommand(rawArgs string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return rawArgs
	}
	return args.Command
}
