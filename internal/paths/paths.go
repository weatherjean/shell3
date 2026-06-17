package paths

import (
	"path/filepath"
)

// ProjectDirName is the per-project runtime directory created under a workdir
// (history, inbox, jobs, subagent transcripts). The single source of this name —
// route every path through the helpers here rather than rebuilding the literal.
const ProjectDirName = ".shell3_project"

// Global holds all paths under ~/.shell3/ (user-scoped, never in repo).
type Global struct {
	Root    string // ~/.shell3/
	LogFile string // ~/.shell3/shell3.log
}

// Local holds paths under ./.shell3_project/ (project-scoped runtime data;
// gitignored via /.shell3_project/ in the repo root .gitignore).
type Local struct {
	Root  string // ./.shell3_project/
	Runs  string // ./.shell3_project/runs/
	Inbox string // ./.shell3_project/inbox.jsonl
}

// NewGlobal returns a Global path set rooted at homeDir/.shell3/.
func NewGlobal(homeDir string) Global {
	root := filepath.Join(homeDir, ".shell3")
	return Global{
		Root:    root,
		LogFile: filepath.Join(root, "shell3.log"),
	}
}

// NewLocal returns a Local path set rooted at cwd/.shell3_project/.
func NewLocal(cwd string) Local {
	root := filepath.Join(cwd, ProjectDirName)
	return Local{
		Root:  root,
		Runs:  filepath.Join(root, "runs"),
		Inbox: filepath.Join(root, "inbox.jsonl"),
	}
}

// AgentsDir is where subagents write their audit-JSONL transcripts, under the
// runtime workdir. The parent writes here and the dashboard reads back from it,
// so both sides must agree — route through this helper, never hand-build it.
func AgentsDir(workdir string) string { return filepath.Join(workdir, ProjectDirName, "agents") }

// AgentTranscript returns the transcript path for a given subagent id.
func AgentTranscript(workdir, id string) string {
	return filepath.Join(AgentsDir(workdir), id+".jsonl")
}

// LastErrorPath is where a failed turn dumps its request/response for debugging.
func LastErrorPath(workdir string) string {
	return filepath.Join(workdir, ProjectDirName, "last_error.json")
}
