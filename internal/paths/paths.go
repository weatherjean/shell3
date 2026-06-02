package paths

import "path/filepath"

// Global holds all paths under ~/.shell3/ (user-scoped, never in repo).
type Global struct {
	Root     string // ~/.shell3/
	Auth     string // ~/.shell3/ai-do-not-read.auth.yaml
	Secrets  string // ~/.shell3/ai-do-not-read.secrets.yaml
	Projects string // ~/.shell3/projects/
	LogFile  string // ~/.shell3/shell3.log
}

// Project holds paths for one project's personal state keyed by UUID.
type Project struct {
	Dir  string // ~/.shell3/projects/<uuid>/
	DB   string // ~/.shell3/projects/<uuid>/shell3.db
	Meta string // ~/.shell3/projects/<uuid>/meta.json
}

// Local holds paths under ./.shell3/ (project-scoped, committed to repo).
type Local struct {
	Root   string // ./.shell3/
	Ref    string // ./.shell3/.ref  (gitignored)
	BGJobs string // ./.shell3/bg.json (gitignored)
}

// NewGlobal returns a Global path set rooted at homeDir/.shell3/.
func NewGlobal(homeDir string) Global {
	root := filepath.Join(homeDir, ".shell3")
	return Global{
		Root:     root,
		Auth:     filepath.Join(root, "ai-do-not-read.auth.yaml"),
		Secrets:  filepath.Join(root, "ai-do-not-read.secrets.yaml"),
		Projects: filepath.Join(root, "projects"),
		LogFile:  filepath.Join(root, "shell3.log"),
	}
}

// NewProject returns the Project path set for the given UUID under g.Projects.
func NewProject(g Global, uuid string) Project {
	dir := filepath.Join(g.Projects, uuid)
	return Project{
		Dir:  dir,
		DB:   filepath.Join(dir, "shell3.db"),
		Meta: filepath.Join(dir, "meta.json"),
	}
}

// NewLocal returns a Local path set rooted at cwd/.shell3/.
func NewLocal(cwd string) Local {
	root := filepath.Join(cwd, ".shell3")
	return Local{
		Root:   root,
		Ref:    filepath.Join(root, ".ref"),
		BGJobs: filepath.Join(root, "bg.json"),
	}
}

// BGLogDir is where bash_bg writes per-job log files. Lives under /tmp so
// the OS clears it on reboot; callers should mkdir before writing.
func BGLogDir() string { return filepath.Join("/tmp", "shell3", "runs") }

// BGLogPath returns the log file path for a given job id.
func BGLogPath(id string) string { return filepath.Join(BGLogDir(), id+".log") }
