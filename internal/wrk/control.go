//go:build unix

package wrk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
)

type ExternalEvent struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Created time.Time `json:"created"`
	Body    string    `json:"body,omitempty"`
	Wake    string    `json:"wake,omitempty"`
}

type NodeSnapshot struct {
	Name            string   `json:"name"`
	Kind            NodeKind `json:"kind"`
	Status          string   `json:"status"`
	Attempts        int      `json:"attempts,omitempty"`
	After           []string `json:"after,omitempty"`
	Event           string   `json:"event,omitempty"`
	Message         string   `json:"message,omitempty"`
	PersistedStatus string   `json:"persisted_status"`
	AttemptDir      string   `json:"attempt_dir,omitempty"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	Result          string   `json:"result,omitempty"`
}

type Snapshot struct {
	Task             string         `json:"task"`
	RunID            string         `json:"run_id"`
	Status           string         `json:"status"`
	Created          time.Time      `json:"created"`
	WorkDir          string         `json:"workdir"`
	RunDir           string         `json:"run_dir"`
	Artifacts        string         `json:"artifacts"`
	Nodes            []NodeSnapshot `json:"nodes"`
	PersistedStatus  string         `json:"persisted_status"`
	ExecutionActive  bool           `json:"execution_active"`
	ObservedAt       time.Time      `json:"observed_at"`
	Deadline         time.Time      `json:"deadline,omitzero"`
	DeadlineExceeded bool           `json:"deadline_exceeded"`
	RecoveryRequired bool           `json:"recovery_required"`
	Message          string         `json:"message,omitempty"`
}

type cancellation struct {
	Created time.Time `json:"created"`
}

// Signal durably records an external event. A later beat may satisfy any wait
// node naming it, including a node whose dependencies are not ready yet.
func Signal(runDir, name, body string) (ExternalEvent, error) {
	if err := validateEventName(name); err != nil {
		return ExternalEvent{}, err
	}
	manifest, _, err := loadRun(runDir)
	if err != nil {
		return ExternalEvent{}, err
	}
	status, err := readFileStatus(filepath.Join(runDir, "status"))
	if err != nil {
		return ExternalEvent{}, err
	}
	if status == "completed" || status == "failed" || status == "cancelled" {
		return ExternalEvent{}, fmt.Errorf("wrk: cannot signal terminal run in status %s", status)
	}
	root := controlRoot(manifest)
	receipt, err := (inbox.Store{Root: root}).Notify(inbox.Request{
		To: workflowTarget(manifest), Source: "local-process", Event: name, Correlation: manifest.RunID, Body: body,
	})
	if err != nil {
		return ExternalEvent{}, err
	}
	return ExternalEvent{ID: receipt.ID, Name: name, Created: time.Now().UTC(), Body: body, Wake: receipt.Wake}, nil
}

// Cancel durably requests cancellation. An active beat watches this marker and
// cancels its child process group; future beats remain terminally cancelled.
func Cancel(runDir string) error {
	manifest, _, err := loadRun(runDir)
	if err != nil {
		return err
	}
	status, err := readFileStatus(filepath.Join(runDir, "status"))
	if err != nil {
		return err
	}
	if status == "cancelled" {
		return nil
	}
	if status == "completed" || status == "failed" {
		return fmt.Errorf("wrk: cannot cancel terminal run in status %s", status)
	}
	path := filepath.Join(runDir, "cancel.json")
	_, statErr := os.Stat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if errors.Is(statErr, os.ErrNotExist) {
		if err := writeJSON(path, cancellation{Created: time.Now().UTC()}); err != nil {
			return err
		}
	}
	lock, err := lockRunBlocking(runDir)
	if err != nil {
		return err
	}
	defer unlockRun(lock)
	status, err = readFileStatus(filepath.Join(runDir, "status"))
	if err != nil {
		return err
	}
	if status == "completed" || status == "failed" {
		_ = os.Remove(path)
		return fmt.Errorf("wrk: cannot cancel terminal run in status %s", status)
	}
	if err := atomicWrite(filepath.Join(runDir, "status"), []byte("cancelled\n")); err != nil {
		return err
	}
	return notifyTerminal(runDir, manifest, "cancelled")
}

// Inspect checks the execution lease as well as durable state. A free lease
// proves no beat or supervised external runner owns this run. Hold a shared
// lease while reading idle state; concurrent inspectors must not look like
// executing beats. Active state is a point-in-time observation, not a promise
// of future progress. Artifact files alone are never evidence of liveness.
func Inspect(runDir string) (Snapshot, error) {
	manifest, def, err := loadRun(runDir)
	if err != nil {
		return Snapshot{}, err
	}
	lease, active, err := inspectLease(runDir)
	if err != nil {
		return Snapshot{}, err
	}
	if lease != nil {
		defer lease.Close()
	}
	status, err := readFileStatus(filepath.Join(runDir, "status"))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		Task: manifest.Task, RunID: manifest.RunID, Status: status, Created: manifest.Created,
		WorkDir: manifest.WorkDir, RunDir: runDir, Artifacts: filepath.Join(runDir, "artifacts"),
		PersistedStatus: status, ExecutionActive: active, ObservedAt: time.Now().UTC(), Deadline: manifest.Deadline,
	}
	if snapshot.Deadline.IsZero() && def.Timeout > 0 {
		snapshot.Deadline = manifest.Created.Add(def.Timeout)
	}
	unfinished := status != "completed" && status != "failed" && status != "cancelled"
	snapshot.DeadlineExceeded = unfinished && !snapshot.Deadline.IsZero() && !snapshot.ObservedAt.Before(snapshot.Deadline)
	interrupted := status == "running" || status == "interrupted"
	for _, node := range def.Nodes {
		nodeDir := filepath.Join(runDir, "nodes", node.Name)
		state, err := readStatus(nodeDir)
		if err != nil {
			return Snapshot{}, err
		}
		switch state {
		case "pending", "running", "interrupted", "waiting", "passed", "failed":
		default:
			return Snapshot{}, fmt.Errorf("wrk: node %s has invalid status %q", node.Name, state)
		}
		interrupted = interrupted || state == "running" || state == "interrupted"
		ns := NodeSnapshot{
			Name: node.Name, Kind: node.Kind, Status: state, Attempts: nextAttempt(nodeDir) - 1,
			After: append([]string(nil), node.After...), Event: node.Event, Message: node.Message,
			PersistedStatus: state,
		}
		if ns.Attempts > 0 && (node.Kind == AgentNode || node.Kind == LoopNode) {
			ns.AttemptDir = filepath.Join(nodeDir, fmt.Sprintf("attempt-%d", ns.Attempts))
			ns.Stdout = filepath.Join(ns.AttemptDir, "stdout.log")
			ns.Stderr = filepath.Join(ns.AttemptDir, "stderr.log")
			ns.Result = filepath.Join(nodeDir, fmt.Sprintf("result-%d.md", ns.Attempts))
		}
		if node.Kind == CommandNode {
			ns.Stdout = filepath.Join(nodeDir, "command.log")
		}
		if !active && (state == "running" || state == "interrupted") {
			ns.Status = "interrupted"
			if !unfinished {
				ns.Status = status
			}
		}
		snapshot.Nodes = append(snapshot.Nodes, ns)
	}
	if unfinished {
		switch {
		case active:
			snapshot.Status = "running"
			snapshot.Message = "An execution owner holds the run lock. This proves ownership, not useful progress; inspect the exact attempt logs."
			if snapshot.DeadlineExceeded {
				snapshot.Message = "The deadline has elapsed but an execution owner still holds the run lock; shutdown may be in progress. Do not estimate completion from the deadline."
			}
		case snapshot.DeadlineExceeded:
			snapshot.Status = "expired"
			snapshot.RecoveryRequired = true
			snapshot.Message = "No execution owner; the deadline has elapsed. A beat will record failure without starting another worker."
		case interrupted:
			snapshot.Status = "interrupted"
			snapshot.RecoveryRequired = true
			snapshot.Message = "No execution owner; the recorded execution was interrupted. Nothing is advancing this run. Use bash_bg to resume with wrk beat, or cancel it. Recovery may repeat work; inspect previous attempts first."
		case status == "ready":
			snapshot.Message = "No execution owner; this run is ready for a beat. Durable state alone does not schedule execution."
		case status == "waiting":
			snapshot.Message = "Waiting for an external event; no execution owner is currently active."
		}
	}
	return snapshot, nil
}

func inspectLease(runDir string) (*os.File, bool, error) {
	f, err := os.Open(filepath.Join(runDir, "beat.lock"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return f, false, nil
}

func loadEvents(runDir string) ([]ExternalEvent, error) {
	dir := filepath.Join(runDir, "events")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wrk: read events: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	var events []ExternalEvent
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var event ExternalEvent
		if err := readJSON(filepath.Join(dir, entry.Name()), &event); err != nil {
			return nil, fmt.Errorf("wrk: read event %s: %w", entry.Name(), err)
		}
		if event.ID+".json" != entry.Name() || validateEventName(event.Name) != nil {
			return nil, fmt.Errorf("wrk: invalid event record %s", entry.Name())
		}
		events = append(events, event)
	}
	return events, nil
}

func ingestSignals(runDir string, manifest Manifest) error {
	root := controlRoot(manifest)
	store := inbox.Store{Root: root}
	target := workflowTarget(manifest)
	if err := store.Recover(target); err != nil {
		return err
	}
	eventDir := filepath.Join(runDir, "events")
	if err := os.MkdirAll(eventDir, 0o700); err != nil {
		return fmt.Errorf("wrk: create event directory: %w", err)
	}
	for {
		delivery, ok, err := store.Claim(target)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		if err := validateEventName(delivery.Message.Event); err != nil {
			return err
		}
		event := ExternalEvent{
			ID: delivery.Message.ID, Name: delivery.Message.Event, Created: delivery.Message.Created, Body: delivery.Message.Body,
		}
		if err := writeJSON(filepath.Join(eventDir, event.ID+".json"), event); err != nil {
			return err
		}
		if err := store.Ack(delivery); err != nil {
			return err
		}
	}
}

func matchingEvent(events []ExternalEvent, name string) *ExternalEvent {
	for i := range events {
		if events[i].Name == name {
			return &events[i]
		}
	}
	return nil
}

func isCancelled(runDir string) bool {
	_, err := os.Stat(filepath.Join(runDir, "cancel.json"))
	return err == nil
}

func validateEventName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return errors.New("wrk: event name is required")
	case len(name) > 256:
		return errors.New("wrk: event name is too long")
	case strings.ContainsAny(name, "\r\n\x00"):
		return errors.New("wrk: event name contains an invalid character")
	}
	return nil
}

func readFileStatus(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	status := strings.TrimSpace(string(data))
	switch status {
	case "ready", "running", "interrupted", "waiting", "completed", "failed", "cancelled":
		return status, nil
	default:
		return "", fmt.Errorf("wrk: invalid run status %q", status)
	}
}
