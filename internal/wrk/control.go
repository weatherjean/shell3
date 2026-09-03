//go:build unix

package wrk

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	Name     string   `json:"name"`
	Kind     NodeKind `json:"kind"`
	Status   string   `json:"status"`
	Attempts int      `json:"attempts,omitempty"`
	After    []string `json:"after,omitempty"`
	Event    string   `json:"event,omitempty"`
	Message  string   `json:"message,omitempty"`
}

type Snapshot struct {
	Task      string         `json:"task"`
	RunID     string         `json:"run_id"`
	Status    string         `json:"status"`
	Created   time.Time      `json:"created"`
	WorkDir   string         `json:"workdir"`
	RunDir    string         `json:"run_dir"`
	Artifacts string         `json:"artifacts"`
	Nodes     []NodeSnapshot `json:"nodes"`
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

// Inspect returns a stable machine-readable view without changing run state.
func Inspect(runDir string) (Snapshot, error) {
	manifest, def, err := loadRun(runDir)
	if err != nil {
		return Snapshot{}, err
	}
	status, err := readFileStatus(filepath.Join(runDir, "status"))
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{
		Task: manifest.Task, RunID: manifest.RunID, Status: status, Created: manifest.Created,
		WorkDir: manifest.WorkDir, RunDir: runDir, Artifacts: filepath.Join(runDir, "artifacts"),
	}
	for _, node := range def.Nodes {
		nodeDir := filepath.Join(runDir, "nodes", node.Name)
		state, err := readStatus(nodeDir)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Nodes = append(snapshot.Nodes, NodeSnapshot{
			Name: node.Name, Kind: node.Kind, Status: state, Attempts: nextAttempt(nodeDir) - 1,
			After: append([]string(nil), node.After...), Event: node.Event, Message: node.Message,
		})
	}
	return snapshot, nil
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
	case "ready", "waiting", "completed", "failed", "cancelled":
		return status, nil
	default:
		return "", fmt.Errorf("wrk: invalid run status %q", status)
	}
}
