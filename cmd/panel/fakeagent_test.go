package main

import (
	"context"
	"net/netip"
	"sync"
	"time"

	"mcpanel/internal/agent/agentapi"
)

type fakeAgent struct {
	mu   sync.Mutex
	bans map[string]time.Duration
}

func newFakeAgent() *fakeAgent { return &fakeAgent{bans: map[string]time.Duration{}} }

func (f *fakeAgent) Health(context.Context) (agentapi.HealthResponse, error) {
	return agentapi.HealthResponse{Status: "ok", Version: "test", Firewall: "noop"}, nil
}

func (f *fakeAgent) Ban(_ context.Context, p netip.Prefix, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bans[p.String()] = d
	return nil
}

func (f *fakeAgent) Unban(_ context.Context, p netip.Prefix) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bans, p.String())
	return nil
}

func (f *fakeAgent) Bans(context.Context) ([]agentapi.Ban, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]agentapi.Ban, 0, len(f.bans))
	for p, d := range f.bans {
		out = append(out, agentapi.Ban{Prefix: p, ExpiresInSeconds: int64(d / time.Second)})
	}
	return out, nil
}

func (f *fakeAgent) banned(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.bans[prefix]
	return ok
}
