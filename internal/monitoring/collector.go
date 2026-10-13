package monitoring

import (
	"context"
	"sync"
)

// collector samples servers and host (Phase A stub: no passes; B1 implements it).
type collector struct {
	d    Deps
	mu   sync.Mutex
	last Snapshot
}

func newCollector(d Deps) *collector { return &collector{d: d} }

var _ = (*collector).deps // Phase A stub: d is used once B1 implements the collector

func (c *collector) deps() Deps { return c.d }

// Run blocks until ctx is done: the first pass runs right at the start, then one every
// collectEvery; every pass goes to onPass (V4).
func (c *collector) Run(ctx context.Context, _ func(context.Context, Snapshot)) { <-ctx.Done() }

// Latest is a copy of the last pass (zero value before the first).
func (c *collector) Latest() Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// Watch switches the server to live mode for liveWindow.
func (c *collector) Watch(int64) {}

// Live is the freshest sample of the server, including 5-s TPS in live mode.
func (c *collector) Live(int64) (ServerSample, bool) { return ServerSample{}, false }
