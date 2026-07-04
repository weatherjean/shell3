package paths

import (
	"path/filepath"
)

// ProjectDirName is the per-project runtime directory created under a workdir
// (conversation history under runs/). The single source of this name —
// route every path through the helpers here rather than rebuilding the literal.
const ProjectDirName = ".shell3_project"

// Global holds all paths under ~/.shell3/ (user-scoped, never in repo).
type Global struct {
	Root       string // ~/.shell3/
	LogFile    string // ~/.shell3/shell3.log
	ConfigFile string // ~/.shell3/shell3.lua (the default config)
}

// Local holds paths under ./.shell3_project/ (project-scoped runtime data;
// gitignored via /.shell3_project/ in the repo root .gitignore).
type Local struct {
	Root string // ./.shell3_project/
	Runs string // ./.shell3_project/runs/
}

// NewGlobal returns a Global path set rooted at homeDir/.shell3/.
func NewGlobal(homeDir string) Global {
	root := filepath.Join(homeDir, ".shell3")
	return Global{
		Root:       root,
		LogFile:    filepath.Join(root, "shell3.log"),
		ConfigFile: filepath.Join(root, "shell3.lua"),
	}
}

// ConfigNamed returns the path of a named config: ~/.shell3/<name>.lua.
func (g Global) ConfigNamed(name string) string {
	return filepath.Join(g.Root, name+".lua")
}

// NewLocal returns a Local path set rooted at cwd/.shell3_project/.
func NewLocal(cwd string) Local {
	root := filepath.Join(cwd, ProjectDirName)
	return Local{
		Root: root,
		Runs: filepath.Join(root, "runs"),
	}
}

// LastErrorPath is where a failed turn dumps its request/response for debugging.
func LastErrorPath(workdir string) string {
	return filepath.Join(workdir, ProjectDirName, "last_error.json")
}
