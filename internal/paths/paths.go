package paths

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Global holds all paths under ~/.shell3/ (user-scoped, never in repo).
type Global struct {
	Root     string // ~/.shell3/
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

// SinkPath returns the per-session sink file (the append-only JSONL
// notification channel) under <workdir>/.shell3/sink/<session>.jsonl. The
// session name is sanitized for filesystem safety — session names look like
// "sub:a1f" or "tg:1234", and the colons (plus path separators, for paranoia)
// are replaced — so the name never escapes the sink directory or names an
// illegal file. Callers should mkdir the parent before writing.
func SinkPath(workdir, session string) string {
	return filepath.Join(workdir, ".shell3", "sink", sanitizeSession(session)+".jsonl")
}

// SockPath returns the per-session Unix-domain socket path. Kept short
// (numeric session id) because macOS caps socket paths at ~104 bytes.
func SockPath(workdir string, sessionID int64) string {
	return filepath.Join(workdir, ".shell3", "sock", fmt.Sprintf("%d.sock", sessionID))
}

// sanitizeSession maps a session name to a filesystem-safe single path
// component: ':' (the namespace separator in names like "sub:a1f") and the
// path separators '/' and '\' become '_'. An empty result falls back to
// "session" so we never produce a dotfile or an empty name.
func sanitizeSession(session string) string {
	r := strings.NewReplacer(":", "_", "/", "_", "\\", "_")
	s := r.Replace(session)
	if s == "" {
		return "session"
	}
	return s
}
