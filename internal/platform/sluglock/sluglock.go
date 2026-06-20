// Package sluglock provides per-slug write serialization — the in-process
// equivalent of the upstream Cloudflare Durable Object. It makes a slug's
// read-modify-write of its comment list atomic within one process.
//
// The interface admits a future distributed implementation (e.g. a PostgreSQL
// advisory lock) for multi-instance deployments without changing callers.
package sluglock

import (
	"context"
	"sync"
)

// Locker serializes work per key.
type Locker interface {
	// With runs fn while holding the lock for key, releasing it afterward.
	With(ctx context.Context, key string, fn func() error) error
}

// Memory is an in-process keyed mutex. Correct for the single-instance default.
type Memory struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// NewMemory creates an in-process keyed locker.
func NewMemory() *Memory {
	return &Memory{locks: make(map[string]*sync.Mutex)}
}

func (m *Memory) lockFor(key string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[key]
	if !ok {
		l = &sync.Mutex{}
		m.locks[key] = l
	}
	return l
}

// With runs fn under the per-key lock. The context is honored before acquiring;
// once acquired, fn runs to completion.
func (m *Memory) With(ctx context.Context, key string, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l := m.lockFor(key)
	l.Lock()
	defer l.Unlock()
	return fn()
}
