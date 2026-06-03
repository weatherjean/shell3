package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/edittool"
)

const (
	editDiffContextLines    = 3
	createdFilePreviewLines = 5
)

func handleEditTool(name, rawArgs, workDir string) string {
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
}

func formatEditResult(r edittool.Result, fullWrite bool) string {
	verb := "Edited"
	if fullWrite && r.Created {
		verb = "Created"
	} else if fullWrite {
		verb = "Overwrote"
	}
	header := fmt.Sprintf("%s %s (+%d -%d, %d→%d bytes)", verb, r.Path, r.Additions, r.Deletions, len(r.OldContent), len(r.NewContent))
	if fullWrite && r.Created {
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

