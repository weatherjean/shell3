package luacfg

import (
	"bufio"
	"os"
	"strings"
)

func loadDotEnv(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			key := strings.TrimSpace(k)
			key = strings.TrimPrefix(key, "export ")
			key = strings.TrimSpace(key)
			out[key] = parseDotEnvValue(v)
		}
	}
	return out, sc.Err()
}

// parseDotEnvValue strips inline comments and a single surrounding quote pair.
// Comment handling runs first, but only strips a `#` that is OUTSIDE quotes so a
// `#` inside a quoted value is preserved. Then a single matching pair of leading
// and trailing quotes (either "..." or '...') is removed.
func parseDotEnvValue(v string) string {
	v = stripInlineComment(v)
	v = strings.TrimSpace(v)
	if len(v) >= 2 {
		q := v[0]
		if (q == '"' || q == '\'') && v[len(v)-1] == q {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// stripInlineComment removes a trailing unquoted `# comment`. A `#` only starts a
// comment when it is outside any quoted span and preceded by whitespace (or is at
// the start of the value); a `#` inside quotes, or one with no preceding space, is
// kept as a literal value character. An unterminated opening quote leaves the
// rest of the line treated as literal (no `#` is stripped), matching the
// preserve-`#`-inside-quotes intent.
func stripInlineComment(v string) string {
	var quote byte // 0 when not inside quotes
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t'):
			return strings.TrimRight(v[:i], " \t")
		}
	}
	return v
}
