package chat

import "strings"

// SplitStatus extracts (provider, model) from a status line shaped as
// "<provider> │ <model>" or "<provider> │ <model> │ <effort>". Returns
// ("", "") if no separator is found.
func SplitStatus(statusLine string) (string, string) {
	parts := strings.SplitN(statusLine, " │ ", 3)
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}
