package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/edittool"
	"github.com/weatherjean/shell3/internal/patchtui"
)

const editDiffPreviewLines = 12

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
	if created || (r.Additions == 0 && r.Deletions == 0) {
		return header
	}
	diff := edittool.UnifiedDiff(r.OldContent, r.NewContent, editDiffPreviewLines)
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
	if r.Created || (r.Additions == 0 && r.Deletions == 0) {
		return header
	}
	diff := edittool.UnifiedDiff(r.OldContent, r.NewContent, editDiffPreviewLines)
	if strings.TrimSpace(diff) == "" {
		return header
	}
	return header + "\n" + diff
}

// colorizeEditOutput renders +/- diff lines with red/green backgrounds so the
// TUI shows a git-diff-style preview. Header line and context lines are dimmed
// to match other tool output. Returns plain ANSI; not consumed by the model.
func colorizeEditOutput(s string) string {
	if s == "" {
		return s
	}
	bgAdd := patchtui.BgRGB(20, 60, 20)
	bgDel := patchtui.BgRGB(70, 20, 20)
	fgAdd := patchtui.FgRGB(180, 230, 180)
	fgDel := patchtui.FgRGB(240, 180, 180)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "+ "):
			lines[i] = bgAdd + fgAdd + l + patchtui.Reset
		case strings.HasPrefix(l, "- "):
			lines[i] = bgDel + fgDel + l + patchtui.Reset
		case l != "":
			lines[i] = patchtui.Dim + l + patchtui.Reset
		}
	}
	return strings.Join(lines, "\n")
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
