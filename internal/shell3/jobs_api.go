package shell3

import "time"

// RunningJobs returns the number of live background commands owned by this
// session.
func (s *Session) RunningJobs() int {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return 0
	}
	rt.jobs.mu.Lock()
	defer rt.jobs.mu.Unlock()
	n := 0
	for _, job := range rt.jobs.jobs {
		if job.parentID == s.name {
			n++
		}
	}
	return n
}

// KilledJob describes one job /superstop killed.
type KilledJob struct {
	ID      string
	Title   string
	Runtime time.Duration
}

// KillAllForStop kills every live background command with completion notices
// suppressed. The returned list supplies the one superstop summary that
// replaces them.
func (rt *Runtime) KillAllForStop() []KilledJob {
	if rt == nil || rt.jobs == nil {
		return nil
	}
	return rt.jobs.killAllForStop()
}
