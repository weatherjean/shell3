//go:build unix

package web

import "sync"

// UsageStore holds the most recent turn's token usage (per turn, not
// accumulated). The bot writes it; the dashboard reads it. Safe for concurrent
// use.
type UsageStore struct {
	mu                        sync.Mutex
	set                       bool
	prompt, completion, total int
}

// NewUsageStore returns an empty store.
func NewUsageStore() *UsageStore { return &UsageStore{} }

// Set records one turn's token totals.
func (u *UsageStore) Set(prompt, completion, total int) {
	u.mu.Lock()
	u.prompt, u.completion, u.total, u.set = prompt, completion, total, true
	u.mu.Unlock()
}

func (u *UsageStore) snapshot() (prompt, completion, total int, ok bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.prompt, u.completion, u.total, u.set
}
