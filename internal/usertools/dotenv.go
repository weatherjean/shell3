package usertools

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// LoadDotEnv reads a KEY=value file. Missing files return an empty map (not
// an error). Returns an error if the file is world- or group-readable on
// Unix — secrets must be 0600.
//
// Format: lines starting with '#' are comments. Blank lines are skipped.
// Values may be wrapped in double quotes; quotes are stripped. No shell
// expansion. Anything after the first '=' is the value (so values may
// themselves contain '=').
func LoadDotEnv(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("dotenv: open: %w", err)
	}
	defer f.Close()

	if runtime.GOOS != "windows" {
		fi, sErr := f.Stat()
		if sErr != nil {
			return nil, fmt.Errorf("dotenv: stat %s: %w", path, sErr)
		}
		mode := fi.Mode().Perm()
		if mode&0o077 != 0 {
			return nil, fmt.Errorf("dotenv: %s has permissions %#o; tighten to 0600 (chmod 600 %s)", path, mode, path)
		}
	}

	out := map[string]string{}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("dotenv: %s:%d: missing '='", path, lineNo)
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			return nil, fmt.Errorf("dotenv: %s:%d: empty key", path, lineNo)
		}
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("dotenv: scan: %w", err)
	}
	return out, nil
}
