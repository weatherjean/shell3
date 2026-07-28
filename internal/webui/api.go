//go:build unix

package webui

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// The read-only JSON API behind the Status view and the jobs list. Every shape
// here is the browser's shape — see webui/src/lib/api.ts for the other side.

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type capabilitiesResp struct {
	Agent    agentResp   `json:"agent"`
	Models   []modelResp `json:"models"`
	Voice    voiceResp   `json:"voice"`
	Imagegen bool        `json:"imagegen"`
}

type agentResp struct {
	Name  string `json:"name"`
	Model string `json:"model"`
	// Status is the provider/model/effort line.
	Status string `json:"status,omitempty"`
	// ContextWindow is the model's token budget.
	ContextWindow int        `json:"contextWindow,omitempty"`
	Tools         []toolResp `json:"tools,omitempty"`
}

// toolResp names a tool and says what it does — a bare pill tells a user
// nothing about what `bash_bg` is.
type toolResp struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// paramResp is one settable model parameter with its current and default value.
type paramResp struct {
	Name        string   `json:"name"`
	Value       string   `json:"value,omitempty"`
	Default     string   `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// usageResp is the last turn's token count.
type usageResp struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

type modelResp struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type voiceResp struct {
	STT bool `json:"stt"`
	TTS bool `json:"tts"`
}

// handleCapabilities tells the browser what this install can actually do, so
// the UI can hide what is not wired rather than offering dead controls.
func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	snap := s.snapshot()

	caps := capabilitiesResp{
		Agent:  agentResp{Name: snap.Agent, Model: snap.Model},
		Models: []modelResp{{ID: snap.Model, Name: snap.Model}},
	}
	if s.media != nil {
		caps.Voice = voiceResp{STT: s.media.Transcribe != nil, TTS: s.media.Speak != nil}
		caps.Imagegen = s.media.Generate != nil
	}
	writeJSON(w, caps)
}

type statusResp struct {
	Version   string `json:"version"`
	ConfigDir string `json:"configDir"`
	Uptime    int    `json:"uptimeSeconds"`

	// Warnings are the non-fatal config problems printed to stderr at load —
	// a skipped skill file, a hook naming no agent. A browser user never sees
	// that terminal, so they surface here.
	Warnings []string `json:"warnings"`
	// GateArmed reports whether a tool-call hook governs the main agent. False
	// means the shell runs ungated, which the interface says out loud.
	GateArmed bool `json:"gateArmed"`
	// SystemPrompt is the effective prompt, host reminders included.
	SystemPrompt string      `json:"systemPrompt,omitempty"`
	Params       []paramResp `json:"params"`
	Usage        *usageResp  `json:"usage,omitempty"`

	Agent     agentResp      `json:"agent"`
	Subagents []subagentResp `json:"subagents"`
	Projects  []projectResp  `json:"projects"`
	Skills    []skillResp    `json:"skills"`
	Cron      []cronResp     `json:"cron"`
	MCP       []mcpResp      `json:"mcp"`
	Jobs      jobsSummary    `json:"jobs"`
}

type subagentResp struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       string `json:"model"`
}

type projectResp struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Workdir     string `json:"workdir"`
}

type skillResp struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type cronResp struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Agent    string `json:"agent"`
	Last     string `json:"last"`
}

type mcpResp struct {
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Tools     int    `json:"tools"`
	Error     string `json:"error,omitempty"`
}

type jobsSummary struct {
	Running  int `json:"running"`
	Capacity int `json:"capacity"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := s.snapshot()
	parts := s.rt.Parts()

	resp := statusResp{
		Version:   s.version,
		ConfigDir: s.configDir,
		Uptime:    int(time.Since(s.started).Seconds()),
		Agent: agentResp{
			Name:          snap.Agent,
			Model:         snap.Model,
			Status:        snap.StatusLine,
			ContextWindow: snap.ContextWindow,
			Tools:         describeTools(snap),
		},
		Warnings:     append([]string{}, snap.Warnings...),
		GateArmed:    snap.ToolHooksOn,
		SystemPrompt: snap.SystemPrompt,
		Params:       describeParams(snap),
		Usage:        s.lastUsage(),
		Jobs:         jobsSummary{Running: s.runningJobs()},
		// Non-nil so an empty collection marshals as [] rather than null —
		// the browser iterates these without a guard.
		Subagents: []subagentResp{},
		Projects:  []projectResp{},
		Skills:    []skillResp{},
		Cron:      []cronResp{},
		MCP:       []mcpResp{},
	}

	if parts == nil {
		writeJSON(w, resp)
		return
	}
	resp.Jobs.Capacity = parts.BackgroundMaxConcurrent()

	for _, sub := range parts.Subagents() {
		resp.Subagents = append(resp.Subagents, subagentResp{
			Name: sub.Name, Description: sub.Description, Model: sub.ModelName,
		})
	}
	for _, project := range parts.Projects() {
		resp.Projects = append(resp.Projects, projectResp{
			Name: project.Name, Description: project.Description, Workdir: project.Workdir,
		})
	}
	for _, skill := range parts.Skills() {
		resp.Skills = append(resp.Skills, skillResp{
			Name: skill.Name, Description: skill.Description,
		})
	}
	for _, server := range parts.MCPStatus() {
		resp.MCP = append(resp.MCP, mcpResp{
			Name: server.Name, Connected: server.Up,
			Tools: server.ToolCount, Error: server.Err,
		})
	}

	// Cron next-run times come from the live scheduler when one is armed;
	// otherwise fall back to the declared jobs so the view is never empty.
	if s.cronSource != nil {
		for _, job := range s.cronSource() {
			resp.Cron = append(resp.Cron, cronResp{
				Name: job.Name, Schedule: job.Schedule,
				Agent: job.Agent, Last: job.LastRun,
			})
		}
	} else {
		for _, job := range s.rt.Cron() {
			resp.Cron = append(resp.Cron, cronResp{
				Name: job.Name, Schedule: job.Schedule, Agent: job.Agent,
			})
		}
	}

	writeJSON(w, resp)
}

func describeTools(snap shell3.Snapshot) []toolResp {
	out := make([]toolResp, 0, len(snap.Tools))
	for _, tool := range snap.Tools {
		out = append(out, toolResp{Name: tool.Name, Description: tool.Description})
	}
	return out
}

func describeParams(snap shell3.Snapshot) []paramResp {
	out := make([]paramResp, 0, len(snap.Params))
	for _, param := range snap.Params {
		out = append(out, paramResp{
			Name: param.Name, Value: param.Value, Default: param.Default,
			Description: param.Description, Enum: param.Enum,
		})
	}
	return out
}

func (s *Server) handleJobs(w http.ResponseWriter, _ *http.Request) {
	jobs := []jobDetail{}
	for _, job := range s.allJobs() {
		jobs = append(jobs, describeJob(job))
	}
	writeJSON(w, map[string]any{"jobs": jobs})
}

// routeJobs dispatches /api/jobs/{id} (detail) and /api/jobs/{id}/cancel.
func (s *Server) routeJobs(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	id, tail, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "missing job id", http.StatusBadRequest)
		return
	}
	if tail == "cancel" {
		s.handleJobCancel(w, id)
		return
	}
	s.handleJobDetail(w, id)
}

// handleJobCancel cancels one job by id; cancelling a subagent cascades to the
// jobs it started.
func (s *Server) handleJobCancel(w http.ResponseWriter, id string) {
	// The job runtime is runtime-wide, so any live session can cancel any job.
	sess := s.anySession()
	if sess == nil || sess.KillJob(id) != nil {
		http.Error(w, "no such job", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"cancelled": true})
}

// handleStop cancels the running turn and its background jobs.
func (s *Server) handleStop(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"result": s.stopTurn()})
}

// handleReload rebuilds the config. Refused mid-turn, since a reload swaps the
// parts a running turn is using.
func (s *Server) handleReload(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	busy := s.turnActive
	s.mu.Unlock()
	if busy {
		http.Error(w, "a turn is running", http.StatusConflict)
		return
	}
	res, err := s.rt.Reload()
	if err != nil {
		writeJSON(w, map[string]string{"result": shell3.ReloadReplyText(res, err)})
		return
	}
	s.resync()
	writeJSON(w, map[string]string{"result": shell3.ReloadReplyText(res, nil)})
}

// recordUsage remembers a turn's token count. A turn that reported nothing
// (cancelled early, or a provider that omits usage) leaves the previous
// reading in place rather than blanking it.
func (s *Server) recordUsage(u turnUsage) {
	if u.Total == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage = &usageResp{Prompt: u.Prompt, Completion: u.Completion, Total: u.Total}
}

func (s *Server) lastUsage() *usageResp {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usage
}

// snapshot reads the current agent's shape from a live session, opening a
// throwaway one when no thread has been used yet.
func (s *Server) snapshot() shell3.Snapshot {
	s.mu.Lock()
	sess := s.recentSess
	s.mu.Unlock()
	if sess == nil {
		var err error
		sess, err = s.sessionFor("default")
		if err != nil {
			return shell3.Snapshot{Agent: "agent"}
		}
	}
	return sess.Snapshot()
}

// anySession returns one live session, or nil when none is open. The job
// runtime is shared across sessions, so any of them is a valid handle onto it.
func (s *Server) anySession() *shell3.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recentSess != nil {
		return s.recentSess
	}
	for _, sess := range s.live {
		return sess
	}
	return nil
}

// allJobs lists every background job. Session.Jobs() reports the whole job
// runtime rather than one session's share, so it must be read once — asking
// each live session would repeat every job per session.
func (s *Server) allJobs() []shell3.JobInfo {
	sess := s.anySession()
	if sess == nil {
		return nil
	}
	return sess.Jobs()
}

func (s *Server) runningJobs() int {
	running := 0
	for _, job := range s.allJobs() {
		if !job.Done {
			running++
		}
	}
	return running
}
