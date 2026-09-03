package paths

import (
	"path/filepath"
	"strings"
)

// ProjectDirName is the per-project runtime directory created under a workdir
// (conversation history under runs/). The single source of this name —
// route every path through the helpers here rather than rebuilding the literal.
const ProjectDirName = ".shell3_project"

// Local holds project-scoped runtime paths under ./.shell3_project/.
type Local struct {
	Root   string // ./.shell3_project/
	Runs   string // ./.shell3_project/runs/
	Errors string // ./.shell3_project/errors.jsonl
}

// NewLocal returns a Local path set rooted at cwd/.shell3_project/.
func NewLocal(cwd string) Local {
	root := filepath.Join(cwd, ProjectDirName)
	return Local{
		Root:   root,
		Runs:   filepath.Join(root, "runs"),
		Errors: filepath.Join(root, "errors.jsonl"),
	}
}

// LastErrorPath is where one session keeps its latest failed provider turn.
// The fallback covers callers without a safe durable session id.
func LastErrorPath(workdir, sessionID string) string {
	root := filepath.Join(workdir, ProjectDirName)
	if sessionID == "" || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID {
		return filepath.Join(root, "last_error.json")
	}
	return filepath.Join(root, "runs", sessionID, "last_error.json")
}

// IsCredentialFile reports common dotenv names as sensitive even though
// shell3 does not load them. The Telegram file tool must not become a generic
// credential-exfiltration path for files used by other software.
func IsCredentialFile(name string) bool {
	lower := strings.ToLower(name)
	return lower == ".env" || strings.HasPrefix(lower, ".env.")
}
