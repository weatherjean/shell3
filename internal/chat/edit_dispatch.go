package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/edittool"
	"github.com/weatherjean/shell3/internal/patchtui"
)

const (
	editDiffContextLines    = 3
	createdFilePreviewLines = 5
)

func handleEditTool(name, rawArgs, workDir string) string {
	switch name {
	case "edit_file":
		var args struct {
			FilePath   string `json:"file_path"`
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		}
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return fmt.Sprintf("error: bad arguments: %v", err)
		}
		res, err := edittool.EditFile(workDir, args.FilePath, args.OldString, args.NewString, args.ReplaceAll)
		if err != nil {
			return fmt.Sprintf("error: %s", err.Error())
		}
		return formatEditResult(res, args.OldString == "")
	case "write_file":
		var args struct {
			FilePath string `json:"file_path"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return fmt.Sprintf("error: bad arguments: %v", err)
		}
		res, err := edittool.WriteFile(workDir, args.FilePath, args.Content)
		if err != nil {
			return fmt.Sprintf("error: %s", err.Error())
		}
		return formatWriteResult(res)
	}
	return fmt.Sprintf("error: unknown edit tool %q", name)
}

func formatEditResult(r edittool.Result, created bool) string {
	verb := "Edited"
	if created {
		verb = "Created"
	}
	header := fmt.Sprintf("%s %s (+%d -%d, %d→%d bytes)", verb, r.Path, r.Additions, r.Deletions, len(r.OldContent), len(r.NewContent))
	if created {
		preview := formatCreatedFilePreview(r.NewContent, createdFilePreviewLines)
		if preview == "" {
			return header
		}
		return header + "\n" + preview
	}
	if r.Additions == 0 && r.Deletions == 0 {
		return header
	}
	diff := edittool.UnifiedDiff(r.OldContent, r.NewContent, editDiffContextLines)
	if strings.TrimSpace(diff) == "" {
		return header
	}
	return header + "\n" + diff
}

func formatWriteResult(r edittool.Result) string {
	verb := "Wrote"
	if r.Created {
		verb = "Created"
	}
	header := fmt.Sprintf("%s %s (+%d -%d, %d→%d bytes)", verb, r.Path, r.Additions, r.Deletions, len(r.OldContent), len(r.NewContent))
	if r.Created {
		preview := formatCreatedFilePreview(r.NewContent, createdFilePreviewLines)
		if preview == "" {
			return header
		}
		return header + "\n" + preview
	}
	if r.Additions == 0 && r.Deletions == 0 {
		return header
	}
	diff := edittool.UnifiedDiff(r.OldContent, r.NewContent, editDiffContextLines)
	if strings.TrimSpace(diff) == "" {
		return header
	}
	return header + "\n" + diff
}

func formatCreatedFilePreview(content string, maxLines int) string {
	if content == "" || maxLines <= 0 {
		return ""
	}
	lines := splitPreviewLines(content)
	if len(lines) == 0 {
		return ""
	}
	displayLines := lines
	if len(displayLines) > maxLines {
		displayLines = displayLines[:maxLines]
	}
	out := make([]string, 0, len(displayLines)+2)
	out = append(out, fmt.Sprintf("@@ -0,0 +1,%d @@", len(lines)))
	for _, line := range displayLines {
		out = append(out, "+"+line)
	}
	if hidden := len(lines) - len(displayLines); hidden > 0 {
		out = append(out, fmt.Sprintf("… %d created lines omitted", hidden))
	}
	return strings.Join(out, "\n")
}

func splitPreviewLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// colorizeEditOutput renders +/- diff lines with red/green backgrounds so the
// TUI shows a git-diff-style preview. Hunk headers and omission markers get a
// faint yellow background so they stand out from dimmed context. Returns plain
// ANSI; not consumed by the model.
func colorizeEditOutput(s string) string {
	if s == "" {
		return s
	}
	bgAdd := patchtui.BgRGB(20, 60, 20)
	bgDel := patchtui.BgRGB(70, 20, 20)
	bgMeta := patchtui.BgRGB(74, 64, 24)
	fgAdd := patchtui.FgRGB(180, 230, 180)
	fgDel := patchtui.FgRGB(240, 180, 180)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case isDiffAddedLine(l):
			lines[i] = bgAdd + fgAdd + l + patchtui.Reset
		case isDiffRemovedLine(l):
			lines[i] = bgDel + fgDel + l + patchtui.Reset
		case isDiffMetaLine(l):
			lines[i] = bgMeta + patchtui.Dim + l + patchtui.Reset
		case l != "":
			lines[i] = patchtui.Dim + l + patchtui.Reset
		}
	}
	return strings.Join(lines, "\n")
}

func isDiffAddedLine(line string) bool {
	return strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")
}

func isDiffRemovedLine(line string) bool {
	return strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")
}

func isDiffMetaLine(line string) bool {
	return strings.HasPrefix(line, "@@ ") || (strings.HasPrefix(line, "… ") && strings.Contains(line, "created lines omitted"))
}

// summarizeEditArgs renders a one-line preview suitable for the TUI header.
func summarizeEditArgs(rawArgs string) string {
	var probe struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &probe); err != nil || probe.FilePath == "" {
		return rawArgs
	}
	return fmt.Sprintf(`file_path=%q`, probe.FilePath)
}
