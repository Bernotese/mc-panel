package defense

import (
	"context"
	"net/netip"
	"sync"
)

// FakeGuard is a test double for LoginGuard. Set the *Err fields to simulate blocks.
type FakeGuard struct {
	mu              sync.Mutex
	Attempts        []Attempt
	LoginErr        error
	SecondFactorErr error
	AdminNetErr     error
}

func (f *FakeGuard) CheckLogin(context.Context, netip.Addr, string) error { return f.LoginErr }
func (f *FakeGuard) CheckSecondFactor(context.Context, netip.Addr, string) error {
	return f.SecondFactorErr
}
func (f *FakeGuard) CheckAdminNetwork(context.Context, netip.Addr) error { return f.AdminNetErr }

func (f *FakeGuard) RecordAttempt(_ context.Context, a Attempt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Attempts = append(f.Attempts, a)
	return nil
}

func (f *FakeGuard) Recorded() []Attempt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Attempt(nil), f.Attempts...)
}
