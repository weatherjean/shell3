// Package kit parses a shell3 kit file: a .sh script whose declarations live
// in #--- fenced YAML comment blocks, each binding to the shell function
// beneath it. Parsing never executes the file.
package kit

import (
	"bytes"
	"fmt"
	"strings"
)

// block is one #--- fenced YAML declaration, with the kit file line numbers of
// its opening and closing fence.
type block struct {
	yaml    []byte
	line    int
	endLine int
}

const fence = "#---"

// scanBlocks finds every #--- fenced comment block. Content lines have their
// "# " (or bare "#") prefix stripped so the remainder is real YAML.
func scanBlocks(src []byte) ([]block, error) {
	lines := strings.Split(string(src), "\n")
	var out []block
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != fence {
			continue
		}
		start := i
		var buf bytes.Buffer
		closed := false
		for i++; i < len(lines); i++ {
			t := strings.TrimSpace(lines[i])
			if t == fence {
				closed = true
				break
			}
			if !strings.HasPrefix(t, "#") {
				return nil, fmt.Errorf("line %d: non-comment line inside a declaration block opened at line %d", i+1, start+1)
			}
			t = strings.TrimPrefix(t, "#")
			buf.WriteString(strings.TrimPrefix(t, " "))
			buf.WriteByte('\n')
		}
		if !closed {
			return nil, fmt.Errorf("line %d: declaration block is never closed (missing %s)", start+1, fence)
		}
		out = append(out, block{yaml: buf.Bytes(), line: start + 1, endLine: i + 1})
	}
	return out, nil
}
