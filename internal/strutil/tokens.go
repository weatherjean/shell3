package strutil

import "fmt"

// FormatTokens renders an estimated token count for chat replies: "~41k" from
// 10k upward (single-k precision is plenty for a context meter), the raw
// count below that.
func FormatTokens(n int) string {
	if n >= 10000 {
		return fmt.Sprintf("~%dk", n/1000)
	}
	return fmt.Sprintf("~%d", n)
}
