package audit

import (
	"context"
	"sync"
)

// MemoryRecorder is a test double that keeps entries in memory.
type MemoryRecorder struct {
	mu      sync.Mutex
	Entries []Entry
}

func (m *MemoryRecorder) Record(_ context.Context, e Entry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries = append(m.Entries, e)
	return nil
}

func (m *MemoryRecorder) Actions() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		out[i] = e.Action
	}
	return out
}
