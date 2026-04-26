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

	switch name {
	case "memory_upsert":
		return handleMemoryUpsert(rawArgs, st)
	case "memory_query":
		return handleMemoryQuery(rawArgs, st)
	case "history_query":
		return handleHistoryQuery(rawArgs, st)
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func handleMemoryUpsert(rawArgs string, st *store.Store) string {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Core  *bool  `json:"core"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.Key == "" {
		return "error: key required"
	}
	if err := st.MemoryUpsert(args.Key, args.Value, args.Core); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if args.Value == "" {
		return "Removed: " + args.Key
	}
	if args.Core != nil && *args.Core {
		return "Stored (core): " + args.Key
	}
	return "Stored: " + args.Key
}

func handleMemoryQuery(rawArgs string, st *store.Store) string {
	var args struct {
		Query    string `json:"query"`
		CoreOnly bool   `json:"core_only"`
		Limit    int    `json:"limit"`
	}
	json.Unmarshal([]byte(rawArgs), &args)

	results, err := st.MemoryQuery(args.Query, args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if len(results) == 0 {
		return "No memories found."
	}
	var sb strings.Builder
	for _, r := range results {
		marker := ""
		if r.Core {
			marker = " (core)"
		}
		fmt.Fprintf(&sb, "[%s%s]: %s\n", r.Key, marker, r.Value)
	}
	return sb.String()
}

func handleHistoryQuery(rawArgs string, st *store.Store) string {
	var args struct {
		Query     string `json:"query"`
		SessionID int64  `json:"session_id"`
		Chunk     int    `json:"chunk"`
		Limit     int    `json:"limit"`
	}
	json.Unmarshal([]byte(rawArgs), &args)

	if args.Query != "" {
		res, err := st.HistorySearch(args.Query, args.Limit)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if res.TotalHits == 0 {
			return "No history found."
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "search hits: %d\n", res.TotalHits)
		for _, h := range res.Hits {
			fmt.Fprintf(&sb, "[session %d chunk %d | %s | %s] %s\n",
				h.SessionID, h.Chunk,
				h.CreatedAt.Format("2006-01-02 15:04"), h.Role, h.Content)
		}
		return sb.String()
	}

	res, err := st.HistoryGet(args.SessionID, args.Chunk)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.SessionID == 0 && len(res.Turns) == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "session %d, chunk %d/%d (started %s)",
		res.SessionID, res.Chunk, res.TotalChunks,
		res.SessionStartedAt.Format("2006-01-02 15:04"))
	if res.PrevSessionID != 0 {
		fmt.Fprintf(&sb, " | prev=%d", res.PrevSessionID)
	}
	if res.NextSessionID != 0 {
		fmt.Fprintf(&sb, " | next=%d", res.NextSessionID)
	}
	sb.WriteByte('\n')
	for _, t := range res.Turns {
		fmt.Fprintf(&sb, "[%s | %s] %s\n",
			t.CreatedAt.Format("2006-01-02 15:04"), t.Role, t.Content)
	}
	return sb.String()
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
