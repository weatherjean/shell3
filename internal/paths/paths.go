package paths

import (
	"fmt"
	"path/filepath"
)

// Global holds all paths under ~/.shell3/ (user-scoped, never in repo).
type Global struct {
	Root    string // ~/.shell3/
	Data    string // ~/.shell3/data/
	DB      string // ~/.shell3/data/shell3.db
	LogFile string // ~/.shell3/shell3.log
}

// Local holds paths under ./.shell3/ (project-scoped runtime scratch; the whole
// folder is gitignored via a "*" and never committed).
type Local struct {
	Root string // ./.shell3/
	Ref  string // ./.shell3/.ref
}

// NewGlobal returns a Global path set rooted at homeDir/.shell3/.
func NewGlobal(homeDir string) Global {
	root := filepath.Join(homeDir, ".shell3")
	data := filepath.Join(root, "data")
	return Global{
		Root:    root,
		Data:    data,
		DB:      filepath.Join(data, "shell3.db"),
		LogFile: filepath.Join(root, "shell3.log"),
	}
}

// NewLocal returns a Local path set rooted at cwd/.shell3/.
func NewLocal(cwd string) Local {
	root := filepath.Join(cwd, ".shell3")
	return Local{
		Root: root,
		Ref:  filepath.Join(root, ".ref"),
	}
}

// BGLogDir is where bash_bg writes per-job log files. Lives under /tmp so
// the OS clears it on reboot; callers should mkdir before writing.
func BGLogDir() string { return filepath.Join("/tmp", "shell3", "runs") }

// BGLogPath returns the log file path for a given job id.
func BGLogPath(id string) string { return filepath.Join(BGLogDir(), id+".log") }

// SockPath returns the per-session Unix-domain socket path. Kept short
// (numeric session id) because macOS caps socket paths at ~104 bytes.
func SockPath(workdir string, sessionID int64) string {
	return filepath.Join(workdir, ".shell3", "sock", fmt.Sprintf("%d.sock", sessionID))
}

// AgentsDir is where subagents write their audit-JSONL transcripts, under the
// runtime workdir. The parent writes here and the dashboard reads back from it,
// so both sides must agree — route through this helper, never hand-build it.
func AgentsDir(workdir string) string { return filepath.Join(workdir, ".shell3", "agents") }

// AgentTranscript returns the transcript path for a given subagent id.
func AgentTranscript(workdir, id string) string {
	return filepath.Join(AgentsDir(workdir), id+".jsonl")
}

// LastErrorPath is where a failed turn dumps its request/response for debugging.
func LastErrorPath(workdir string) string {
	return filepath.Join(workdir, ".shell3", "last_error.json")
}
