// Package schedule turns Lisp calendar declarations into durable wrk runs.
// It owns clocks and the execution ledger, never workflow semantics.
package schedule

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	robcron "github.com/robfig/cron/v3"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/wrk"
)

const failureLimit = 4096
const startingGrace = 30 * time.Second

// Job is a resolved schedule whose wrkfile has already passed strict parsing.
type Job struct {
	Config  lispconfig.Schedule
	Wrkfile string
	Task    string
}

// Resolve validates every referenced wrkfile and anchors its path beside the
// shell3.lisp that names it.
func Resolve(configPath string, cfg *lispconfig.Config) ([]Job, error) {
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve config: %w", err)
	}
	jobs := make([]Job, 0, len(cfg.Schedules))
	for _, declaration := range cfg.Schedules {
		path := declaration.Wrkfile
		if !filepath.IsAbs(path) {
			path = filepath.Join(filepath.Dir(configPath), path)
		}
		path, err = filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("schedule %q: resolve wrkfile: %w", declaration.Name, err)
		}
		definition, err := wrk.Load(path, cfg)
		if err != nil {
			return nil, fmt.Errorf("schedule %q: %w", declaration.Name, err)
		}
		jobs = append(jobs, Job{Config: declaration, Wrkfile: path, Task: definition.Name})
	}
	return jobs, nil
}

// Executor starts and advances named schedule invocations. It is shared by
// the resident clock and the explicit one-shot command.
type Executor struct {
	ConfigPath string
	WorkDir    string
	StateRoot  string
	Shell3Bin  string
	Jobs       map[string]Job
	Store      *runs.Store
	Log        applog.Logger
}

func NewExecutor(configPath, workDir string, cfg *lispconfig.Config, store *runs.Store, log applog.Logger) (*Executor, error) {
	if store == nil {
		return nil, errors.New("schedule: runs store is required")
	}
	configPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve config: %w", err)
	}
	workDir, err = filepath.Abs(workDir)
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve workdir: %w", err)
	}
	jobs, err := Resolve(configPath, cfg)
	if err != nil {
		return nil, err
	}
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("schedule: resolve shell3 executable: %w", err)
	}
	byName := make(map[string]Job, len(jobs))
	for _, job := range jobs {
		byName[job.Config.Name] = job
	}
	if log == nil {
		log = applog.Noop{}
	}
	return &Executor{
		ConfigPath: configPath, WorkDir: workDir,
		StateRoot: filepath.Join(workDir, ".shell3_project", "wrk"), Shell3Bin: executable,
		Jobs: byName, Store: store, Log: log,
	}, nil
}

// Run starts one named invocation and drives it until terminal or waiting.
// A waiting workflow remains running in the ledger and is reconciled later.
func (e *Executor) Run(ctx context.Context, name, trigger string, scheduledAt time.Time) (runs.ScheduleRun, error) {
	job, ok := e.Jobs[name]
	if !ok {
		return runs.ScheduleRun{}, fmt.Errorf("schedule: unknown schedule %q", name)
	}
	if trigger == "" {
		trigger = "manual"
	}
	if scheduledAt.IsZero() {
		scheduledAt = time.Now().UTC()
	}
	runID, err := newRunID()
	if err != nil {
		return runs.ScheduleRun{}, fmt.Errorf("schedule: create run id: %w", err)
	}
	runDir := filepath.Join(e.StateRoot, job.Task, runID)
	record := runs.ScheduleRun{
		ID: runID, Schedule: name, Task: job.Task, Trigger: trigger,
		RunDir: runDir, OutputPath: filepath.Join(runDir, "artifacts", job.Config.Output),
		ScheduledAt: scheduledAt, StartedAt: time.Now().UTC(), Status: "running",
	}
	if err := e.Store.StartScheduleRun(record, job.Config.Overlap); err != nil {
		if errors.Is(err, runs.ErrScheduleOverlap) {
			e.Log.Info("schedule fire skipped", "schedule", name, "event", "schedule.skipped", "reason", "overlap")
		}
		return runs.ScheduleRun{}, err
	}
	e.Log.Info("schedule run started", "schedule", name, "event", "schedule.started", "run", runID, "task", job.Task, "output", record.OutputPath)
	_, err = wrk.Start(e.ConfigPath, job.Wrkfile, wrk.StartOptions{
		StateRoot: e.StateRoot, RunID: runID, Shell3Bin: e.Shell3Bin,
		Request: job.Config.Request, NotifyTo: job.Config.Notify,
		NotifyState: filepath.Join(e.WorkDir, ".shell3_project"), Timeout: job.Config.Timeout,
		RequiredOutput: job.Config.Output, ExpectedTask: job.Task,
	})
	if err != nil {
		return e.fail(record, err)
	}
	return e.drive(ctx, record)
}

// Resume advances an already-admitted invocation from its immutable wrk
// snapshot. It does not need the schedule to remain in the current config.
func (e *Executor) Resume(ctx context.Context, record runs.ScheduleRun) (runs.ScheduleRun, error) {
	if record.Status != "running" {
		return record, nil
	}
	return e.drive(ctx, record)
}

func (e *Executor) drive(ctx context.Context, record runs.ScheduleRun) (runs.ScheduleRun, error) {
	for {
		if _, err := os.Stat(record.RunDir); err != nil {
			if errors.Is(err, os.ErrNotExist) && time.Since(record.StartedAt) < startingGrace {
				return record, nil
			}
			return e.fail(record, fmt.Errorf("scheduled workflow state %s: %w", record.RunDir, err))
		}
		snapshot, err := wrk.Inspect(record.RunDir)
		if err != nil {
			return e.fail(record, err)
		}
		switch snapshot.Status {
		case "completed", "failed", "cancelled":
			return e.finishTerminal(ctx, record, snapshot.Status, nil)
		}
		beat, beatErr := wrk.Beat(ctx, record.RunDir)
		if beatErr != nil && errors.Is(beatErr, context.DeadlineExceeded) && ctx.Err() == nil {
			// The invocation deadline cancelled its active process. A second
			// beat observes the expired durable deadline and records failure.
			beat, beatErr = wrk.Beat(context.Background(), record.RunDir)
		}
		switch beat.Status {
		case "completed", "failed", "cancelled":
			return e.finishTerminal(ctx, record, beat.Status, beatErr)
		}
		if beatErr != nil {
			if ctx.Err() != nil || errors.Is(beatErr, wrk.ErrBeatOwned) {
				return record, nil
			}
			return record, beatErr
		}
		if beat.Status == "waiting" {
			return record, nil
		}
	}
}

// finishTerminal retries the idempotent wrk notice before finalizing the
// schedule ledger. A transient persistence failure leaves the row running so
// the resident manager can retry it after restart or on its next sweep.
func (e *Executor) finishTerminal(ctx context.Context, record runs.ScheduleRun, status string, cause error) (runs.ScheduleRun, error) {
	confirmation, confirmationErr := wrk.Beat(ctx, record.RunDir)
	if confirmation.Status != "" {
		status = confirmation.Status
	}
	notified, noticeErr := wrk.TerminalNoticePersisted(record.RunDir)
	if noticeErr != nil {
		return record, noticeErr
	}
	if !notified {
		if ctx.Err() != nil || errors.Is(confirmationErr, wrk.ErrBeatOwned) {
			return record, nil
		}
		if confirmationErr != nil {
			return record, confirmationErr
		}
		return record, errors.New("schedule: terminal workflow notice is not persisted")
	}
	switch status {
	case "completed":
		return e.complete(record)
	case "failed", "cancelled":
		if cause == nil {
			cause = confirmationErr
		}
		if cause == nil {
			cause = fmt.Errorf("workflow ended in status %s", status)
		}
		return e.fail(record, cause)
	default:
		return record, fmt.Errorf("schedule: expected terminal workflow, got %s", status)
	}
}

func (e *Executor) complete(record runs.ScheduleRun) (runs.ScheduleRun, error) {
	if err := e.Store.FinishScheduleRun(record.ID, "done", ""); err != nil {
		return record, err
	}
	record.Status, record.FinishedAt = "done", time.Now().UTC()
	e.Log.Info("schedule run finished", "schedule", record.Schedule, "event", "schedule.done", "run", record.ID, "output", record.OutputPath)
	return record, nil
}

func (e *Executor) fail(record runs.ScheduleRun, cause error) (runs.ScheduleRun, error) {
	failure := boundedError(cause)
	if err := e.Store.FinishScheduleRun(record.ID, "failed", failure); err != nil {
		return record, fmt.Errorf("%v; schedule ledger: %w", cause, err)
	}
	record.Status, record.Error, record.FinishedAt = "failed", failure, time.Now().UTC()
	e.Log.Error("schedule run failed", cause, "schedule", record.Schedule, "event", "schedule.failed", "run", record.ID, "output", record.OutputPath)
	return record, cause
}

func boundedError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > failureLimit {
		return value[:failureLimit]
	}
	return value
}

func newRunID() (string, error) {
	var raw [6]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(raw[:]), nil
}

// Manager owns the project schedule clock. Its advisory lock prevents a
// Telegram adapter and headless service from both firing the same entries.
type Manager struct {
	ctx      context.Context
	cancel   context.CancelFunc
	executor *Executor
	clock    *robcron.Cron
	lock     *os.File
	wg       sync.WaitGroup
	mu       sync.Mutex
	reported map[string]string
	once     sync.Once
}

func Start(parent context.Context, configPath, workDir string, cfg *lispconfig.Config, store *runs.Store, log applog.Logger) (*Manager, error) {
	executor, err := NewExecutor(configPath, workDir, cfg, store, log)
	if err != nil {
		return nil, err
	}
	running, err := executor.Store.RunningScheduleRuns()
	if err != nil {
		return nil, err
	}
	if len(executor.Jobs) == 0 && len(running) == 0 {
		return &Manager{executor: executor}, nil
	}
	lock, err := acquire(filepath.Join(workDir, ".shell3_project", "schedule.lock"))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{
		ctx: ctx, cancel: cancel, executor: executor, clock: robcron.New(), lock: lock,
		reported: map[string]string{},
	}
	for _, job := range executor.Jobs {
		job := job
		spec := "CRON_TZ=" + job.Config.Timezone + " " + job.Config.Cron
		if _, err := m.clock.AddFunc(spec, func() { m.launch(job.Config.Name, "cron", time.Now().UTC()) }); err != nil {
			m.Close()
			return nil, fmt.Errorf("schedule %q: arm cron: %w", job.Config.Name, err)
		}
	}
	m.reconcileRecords(running)
	m.wg.Add(1)
	go m.reconcileLoop()
	m.clock.Start()
	executor.Log.Info("schedule host started", "event", "schedule.host_started", "schedules", len(executor.Jobs))
	return m, nil
}

func (m *Manager) launch(name, trigger string, at time.Time) {
	if m.ctx == nil || m.ctx.Err() != nil {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		run, err := m.executor.Run(m.ctx, name, trigger, at)
		if err != nil && !errors.Is(err, runs.ErrScheduleOverlap) && run.Status != "failed" && m.ctx.Err() == nil {
			m.executor.Log.Error("schedule invocation returned", err, "schedule", name, "event", "schedule.invoke_error", "run", run.ID)
		}
	}()
}

func (m *Manager) reconcile() {
	running, err := m.executor.Store.RunningScheduleRuns()
	if err != nil {
		m.report("ledger", "schedule recovery failed", err, "event", "schedule.recovery_failed")
		return
	}
	m.clearReported("ledger")
	m.reconcileRecords(running)
}

func (m *Manager) reconcileRecords(running []runs.ScheduleRun) {
	for _, record := range running {
		record := record
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			run, err := m.executor.Resume(m.ctx, record)
			if err != nil && run.Status != "failed" && m.ctx.Err() == nil {
				m.report("run:"+record.ID, "schedule recovery returned", err, "schedule", record.Schedule, "event", "schedule.recovery_error", "run", record.ID)
				return
			}
			m.clearReported("run:" + record.ID)
		}()
	}
}

func (m *Manager) report(key, message string, err error, fields ...any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reported[key] == err.Error() {
		return
	}
	m.executor.Log.Error(message, err, fields...)
	m.reported[key] = err.Error()
}

func (m *Manager) clearReported(key string) {
	m.mu.Lock()
	delete(m.reported, key)
	m.mu.Unlock()
}

func (m *Manager) reconcileLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reconcile()
		}
	}
}

func (m *Manager) Close() {
	if m == nil {
		return
	}
	m.once.Do(func() {
		if m.clock != nil {
			stop := m.clock.Stop()
			<-stop.Done()
		}
		if m.cancel != nil {
			m.cancel()
		}
		m.wg.Wait()
		if m.lock != nil {
			_ = syscall.Flock(int(m.lock.Fd()), syscall.LOCK_UN)
			_ = m.lock.Close()
		}
	})
}

func acquire(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("schedule: create state root: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("schedule: open owner lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, errors.New("schedule: another persistent shell3 process owns this project's schedules")
	}
	return f, nil
}

// SameDeclarations reports whether a live adapter may reload without changing
// its armed clock. Schedule changes require a process restart in version 1.
func SameDeclarations(a, b []lispconfig.Schedule) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
