package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/fsx"
)

const (
	defaultReadLimit = 2000             // lines
	maxReadBytes     = 50 * 1024        // 50 KB across emitted lines
	maxReadFileBytes = 10 * 1024 * 1024 // 10 MB ceiling on the file read into memory
	readSampleBytes  = 4096             // bytes sniffed for binary detection
)

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}

// handleReadTool reads a text file with offset/limit paging and a dual
// line/byte cap, returning the raw file text (no line-number gutter, so the
// model can copy substrings straight into edit_file) plus a continuation footer
// when truncated. Binary files are refused with a redirect to read_media.
func handleReadTool(ctx context.Context, argsJSON, workDir string) string {
	var a readArgs
	if err := json.Unmarshal([]byte(argsJSON), &a); err != nil {
		return "error: invalid read arguments: " + err.Error()
	}
	if strings.TrimSpace(a.Path) == "" {
		return "error: read requires a non-empty path"
	}
	if a.Offset <= 0 {
		a.Offset = 1
	}
	if a.Limit <= 0 {
		a.Limit = defaultReadLimit
	}

	path := resolveReadPath(a.Path, workDir)
	content, err := fsx.ReadTextFile(ctx, path)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return "error: file not found: " + a.Path
		case errors.Is(err, fsx.ErrIsDir):
			return "error: " + a.Path + " is a directory; use bash ls"
		}
		return "error: " + err.Error()
	}
	// Size ceiling, applied to the read content (post-read, not a pre-stat).
	// Redirect to a streaming bash extractor for anything bigger.
	if len(content) > maxReadFileBytes {
		return fmt.Sprintf("error: %s is %d MB, exceeds the %d MB read limit; use bash (sed -n / head) to extract the part you need",
			a.Path, len(content)/(1024*1024), maxReadFileBytes/(1024*1024))
	}
	if isBinary([]byte(content)) {
		return "error: binary file; use read_media for images/audio, or bash xxd for raw bytes"
	}

	return renderRead(content, a.Offset, a.Limit)
}

// resolveReadPath expands ~ and resolves a relative path against workDir.
func resolveReadPath(p, workDir string) string {
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(workDir, p)
}

// isBinary sniffs up to readSampleBytes: any NUL byte, or >30% non-printable
// bytes, marks the content binary.
func isBinary(data []byte) bool {
	sample := data
	if len(sample) > readSampleBytes {
		sample = sample[:readSampleBytes]
	}
	if len(sample) == 0 {
		return false
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	nonPrint := 0
	for _, b := range sample {
		if b < 0x09 || (b > 0x0d && b < 0x20) || b == 0x7f {
			nonPrint++
		}
	}
	// Require at least two non-printable bytes before applying the ratio: a single
	// stray control byte in a tiny text file shouldn't flag it binary. Above that,
	// the >30% ratio applies at any size, so a small file that is mostly control
	// bytes (e.g. a short compiled/compressed fragment with no NUL) is still
	// caught — the old "skip files under 32 bytes" floor let those slip through as
	// mojibake. High bytes (0x80+) are intentionally not counted, so valid UTF-8
	// is never misflagged.
	if nonPrint < 2 {
		return false
	}
	return nonPrint*100/len(sample) > 30
}

// renderRead applies offset (1-indexed) + dual line/byte cap and appends a
// continuation footer when more remains.
func renderRead(content string, offset, limit int) string {
	hadTrailingNewline := strings.HasSuffix(content, "\n")
	lines := strings.Split(content, "\n")
	// A trailing newline yields a final empty element; drop it for counting.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)
	if offset > total {
		if total == 0 {
			return "" // empty file
		}
		return fmt.Sprintf("error: offset %d is beyond end of file (%d lines)", offset, total)
	}

	var sb strings.Builder
	bytesOut, last := 0, offset-1
	for i := offset - 1; i < total && i < offset-1+limit; i++ {
		ln := lines[i]
		if i == offset-1 && len(ln)+1 > maxReadBytes {
			return fmt.Sprintf("error: line %d is %dKB, exceeds the %dKB line limit; use bash: sed -n '%dp' <path> | head -c %d",
				offset, len(ln)/1024, maxReadBytes/1024, offset, maxReadBytes)
		}
		if bytesOut+len(ln)+1 > maxReadBytes {
			break
		}
		sb.WriteString(ln)
		sb.WriteByte('\n')
		bytesOut += len(ln) + 1
		last = i + 1
	}
	out := sb.String()
	if last < total {
		out += fmt.Sprintf("\n[Showing lines %d-%d of %d. Use offset=%d to continue.]", offset, last, total, last+1)
	} else if !hadTrailingNewline {
		// We emitted through the real end of a file that had no final newline;
		// don't fabricate one, so a whole-tail copy still exact-matches in
		// edit_file (the read tool's paste-fidelity promise).
		out = strings.TrimSuffix(out, "\n")
	}
	return out
}
