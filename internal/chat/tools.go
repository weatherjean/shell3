package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

// minPruneBytes gates prune_tool_result: small results aren't worth pruning.
const minPruneBytes = 500

// handlePruneToolResult replaces the content of a prior tool message
// (identified by tool_call_id) with a short stub. Mutates both slices in place.
// Refuses small results and results that look like errors.
func handlePruneToolResult(rawArgs string, slices ...[]llm.Message) string {
	var args struct {
		ToolCallID string `json:"tool_call_id"`
		Reason     string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.ToolCallID == "" {
		return "error: tool_call_id required"
	}
	if args.Reason == "" {
		return "error: reason required"
	}
	stem := fmt.Sprintf("pruned: %s", args.Reason)
	return pruneToolResultByID(args.ToolCallID, stem, slices...)
}

// pruneToolResultByID is the shared core: locates the tool message by id,
// gates on size/error, replaces content with a stub `[<stem> — original was N bytes]`,
// and mutates every provided slice that holds the message. Returns a
// human-readable status string suitable for both the model tool and the
// user-facing slash command.
func pruneToolResultByID(toolCallID, stem string, slices ...[]llm.Message) string {
	var target *llm.Message
	var name string
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				target = &msgs[i]
				name = msgs[i].Name
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("error: no tool result with id %q in conversation", toolCallID)
	}

	content := target.Content
	if len(content) < minPruneBytes {
		return fmt.Sprintf("error: result is %d bytes; below %d-byte prune threshold", len(content), minPruneBytes)
	}
	if looksLikeError(content) {
		return "error: refusing to prune a result that looks like a tool error"
	}

	stub := fmt.Sprintf("[%s — original was %d bytes]", stem, len(content))

	count := 0
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				msgs[i].Content = stub
				count++
			}
		}
	}
	if count == 0 {
		return "error: failed to update message content"
	}
	return fmt.Sprintf("Pruned result of %s (id=%s): freed %d bytes", name, toolCallID, len(content)-len(stub))
}

func looksLikeError(s string) bool {
	t := strings.TrimSpace(s)
	// Skip the synthetic [tool_call_id=...] header line if present so the
	// real payload's first line is what we inspect.
	if strings.HasPrefix(t, "[tool_call_id=") {
		if nl := strings.IndexByte(t, '\n'); nl >= 0 {
			t = strings.TrimSpace(t[nl+1:])
		} else {
			return false
		}
	}
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	return strings.HasPrefix(low, "error:") || strings.HasPrefix(low, "error ")
}

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
	case "memory_list":
		return handleMemoryList(rawArgs, st)
	case "memory_search":
		return handleMemorySearch(rawArgs, st)
	case "history_get":
		return handleHistoryGet(rawArgs, st)
	case "history_search":
		return handleHistorySearch(rawArgs, st)
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

func handleMemoryList(rawArgs string, st *store.Store) string {
	var args struct {
		CoreOnly bool `json:"core_only"`
		Limit    int  `json:"limit"`
	}
	json.Unmarshal([]byte(rawArgs), &args)
	results, err := st.MemoryQuery("", args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return renderMemories(results)
}

func handleMemorySearch(rawArgs string, st *store.Store) string {
	var args struct {
		Terms    []string `json:"terms"`
		Match    string   `json:"match"`
		CoreOnly bool     `json:"core_only"`
		Limit    int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if len(args.Terms) == 0 {
		return "error: terms[] required (one concept per element)"
	}
	expr := store.BuildFTSExpr(args.Terms, args.Match == "all")
	if expr == "" {
		return "No memories found."
	}
	results, err := st.MemorySearchExpr(expr, args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	return renderMemories(results)
}

func renderMemories(results []store.MemoryEntry) string {
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

func handleHistoryGet(rawArgs string, st *store.Store) string {
	var args struct {
		SessionID int64 `json:"session_id"`
		Chunk     int   `json:"chunk"`
	}
	json.Unmarshal([]byte(rawArgs), &args)
	chunk := args.Chunk
	if chunk > 0 {
		chunk--
	}
	res, err := st.HistoryGet(args.SessionID, chunk)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.SessionID == 0 && len(res.Turns) == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "session %d, chunk %d/%d (started %s)",
		res.SessionID, res.Chunk+1, res.TotalChunks,
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

func handleHistorySearch(rawArgs string, st *store.Store) string {
	var args struct {
		Terms []string `json:"terms"`
		Match string   `json:"match"`
		Limit int      `json:"limit"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if len(args.Terms) == 0 {
		return "error: terms[] required (one concept per element)"
	}
	expr := store.BuildFTSExpr(args.Terms, args.Match == "all")
	if expr == "" {
		return "No history found."
	}
	res, err := st.HistorySearchExpr(expr, args.Limit)
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
			h.SessionID, h.Chunk+1,
			h.CreatedAt.Format("2006-01-02 15:04"), h.Role, h.Content)
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
