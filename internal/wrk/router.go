//go:build unix

package wrk

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
)

const routeVersion = 1

type route struct {
	Version int       `json:"version"`
	Target  string    `json:"target"`
	Task    string    `json:"task"`
	RunID   string    `json:"run_id"`
	RunDir  string    `json:"run_dir"`
	Created time.Time `json:"created"`
}

type invalidRouteError struct{ err error }

func (e *invalidRouteError) Error() string { return e.err.Error() }
func (e *invalidRouteError) Unwrap() error { return e.err }

func invalidRoute(err error) error { return &invalidRouteError{err: err} }

func controlRoot(manifest Manifest) string {
	if manifest.NotifyState != "" {
		return manifest.NotifyState
	}
	return filepath.Join(manifest.WorkDir, ".shell3_project")
}

func workflowTarget(manifest Manifest) string {
	return "wrk:" + manifest.Task + "/" + manifest.RunID
}

func registerRun(runDir string, manifest Manifest) error {
	runDir, err := filepath.Abs(runDir)
	if err != nil {
		return fmt.Errorf("wrk: resolve registered run: %w", err)
	}
	root := controlRoot(manifest)
	dir := filepath.Join(root, "wrk-routes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("wrk: create route registry: %w", err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "registry.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("wrk: open route registry lock: %w", err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("wrk: lock route registry: %w", err)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	r := route{
		Version: routeVersion, Target: workflowTarget(manifest), Task: manifest.Task,
		RunID: manifest.RunID, RunDir: runDir, Created: manifest.Created,
	}
	path := routePath(root, r.Target)
	var existing route
	if err := readJSON(path, &existing); err == nil {
		if existing == r {
			return nil
		}
		return fmt.Errorf("wrk: route %s is already registered to %s", r.Target, existing.RunDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("wrk: read route %s: %w", r.Target, err)
	}
	if err := writeJSON(path, r); err != nil {
		return fmt.Errorf("wrk: register route %s: %w", r.Target, err)
	}
	return nil
}

func routePath(root, target string) string {
	sum := sha256.Sum256([]byte(target))
	return filepath.Join(root, "wrk-routes", hex.EncodeToString(sum[:])+".json")
}

func resolveRoute(root, target string) (route, error) {
	if !strings.HasPrefix(target, "wrk:") {
		return route{}, fmt.Errorf("wrk: unsupported wake target %q", target)
	}
	var r route
	if err := readJSON(routePath(root, target), &r); err != nil {
		return route{}, fmt.Errorf("wrk: resolve route %s: %w", target, err)
	}
	if r.Version != routeVersion || r.Target != target || r.RunDir == "" {
		return route{}, invalidRoute(fmt.Errorf("wrk: invalid route record for %s", target))
	}
	manifest, _, err := loadRun(r.RunDir)
	if err != nil {
		return route{}, invalidRoute(fmt.Errorf("wrk: validate route %s: %w", target, err))
	}
	if workflowTarget(manifest) != target || manifest.Task != r.Task || manifest.RunID != r.RunID {
		return route{}, invalidRoute(fmt.Errorf("wrk: route record does not match run %s", target))
	}
	return r, nil
}

func registeredTargets(root string) ([]string, error) {
	dir := filepath.Join(root, "wrk-routes")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("wrk: read route registry: %w", err)
	}
	var targets []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var r route
		if err := readJSON(filepath.Join(dir, entry.Name()), &r); err != nil {
			return nil, fmt.Errorf("wrk: read route %s: %w", entry.Name(), err)
		}
		if r.Version != routeVersion || routePath(root, r.Target) != filepath.Join(dir, entry.Name()) {
			return nil, fmt.Errorf("wrk: invalid route record %s", entry.Name())
		}
		targets = append(targets, r.Target)
	}
	sort.Strings(targets)
	return targets, nil
}

// Router turns wake hints into workflow beats. Hints are advisory; startup and
// periodic reconciliation against the route registry provide durable recovery.
type Router struct {
	ctx      context.Context
	cancel   context.CancelFunc
	root     string
	hints    <-chan string
	sem      chan struct{}
	loop     sync.WaitGroup
	jobs     sync.WaitGroup
	mu       sync.Mutex
	active   map[string]bool
	reported map[string]string
	log      applog.Logger
	logClose io.Closer
	once     sync.Once
}

func StartRouter(parent context.Context, root string, hints <-chan string, log applog.Logger) (*Router, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("wrk: resolve router state: %w", err)
	}
	targets, err := registeredTargets(root)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	var logClose io.Closer
	if log == nil {
		log, logClose, err = applog.Open(filepath.Join(root, "errors.jsonl"))
		if err != nil {
			cancel()
			return nil, err
		}
	}
	r := &Router{
		ctx: ctx, cancel: cancel, root: root, hints: hints,
		sem: make(chan struct{}, 4), active: map[string]bool{}, reported: map[string]string{}, log: log, logClose: logClose,
	}
	for _, target := range targets {
		r.enqueue(target)
	}
	r.loop.Add(1)
	go r.run()
	return r, nil
}

func (r *Router) run() {
	defer r.loop.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case target, ok := <-r.hints:
			if !ok {
				r.hints = nil
				continue
			}
			if strings.HasPrefix(target, "wrk:") {
				r.enqueue(target)
			}
		case <-ticker.C:
			targets, err := registeredTargets(r.root)
			if err != nil {
				r.report("registry", err)
				continue
			}
			for _, target := range targets {
				r.enqueue(target)
			}
		}
	}
}

func (r *Router) enqueue(target string) {
	r.mu.Lock()
	if r.active[target] || r.ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	r.active[target] = true
	r.jobs.Add(1)
	r.mu.Unlock()
	go func() {
		defer r.jobs.Done()
		defer func() {
			r.mu.Lock()
			delete(r.active, target)
			r.mu.Unlock()
		}()
		select {
		case r.sem <- struct{}{}:
			defer func() { <-r.sem }()
		case <-r.ctx.Done():
			return
		}
		if err := r.drive(target); err != nil && r.ctx.Err() == nil {
			var invalid *invalidRouteError
			if errors.As(err, &invalid) {
				if quarantineErr := quarantineRoute(r.root, target); quarantineErr != nil {
					err = fmt.Errorf("%w; quarantine failed: %v", err, quarantineErr)
				}
			}
			r.report(target, err)
		} else if err == nil {
			r.clearReported(target)
		}
	}()
}

func quarantineRoute(root, target string) error {
	path := routePath(root, target)
	if err := os.Rename(path, path+".invalid"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("wrk: quarantine route %s: %w", target, err)
	}
	return nil
}

func (r *Router) drive(target string) error {
	route, err := resolveRoute(r.root, target)
	if err != nil {
		return err
	}
	for {
		snapshot, err := Inspect(route.RunDir)
		if err != nil {
			return err
		}
		switch snapshot.Status {
		case "completed", "failed", "cancelled":
			return nil
		}
		result, err := Beat(r.ctx, route.RunDir)
		if err != nil {
			if errors.Is(err, ErrBeatOwned) {
				return nil
			}
			if result.Status == "failed" || result.Status == "cancelled" {
				return nil
			}
			return err
		}
		if result.Status != "ready" {
			return nil
		}
	}
}

func (r *Router) report(target string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.reported[target] == err.Error() {
		return
	}
	r.log.Error("workflow route failed", err, "source", "wrk-router", "event", "route_failed", "target", target)
	r.reported[target] = err.Error()
}

func (r *Router) clearReported(target string) {
	r.mu.Lock()
	delete(r.reported, target)
	r.mu.Unlock()
}

func (r *Router) Close() {
	r.once.Do(func() {
		r.cancel()
		r.loop.Wait()
		r.jobs.Wait()
		if r.logClose != nil {
			_ = r.logClose.Close()
		}
	})
}
