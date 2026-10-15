package backups

import (
	"sync"
	"time"
)

// manualEvery is the cooldown between two manual backups of one server (SEC-04, SEC-25):
// back-to-back manual backups would fill the repository and keep a running server in
// save-off. A manual backup that fails does not count.
const manualEvery = 10 * time.Minute

// cooldowns remembers when the last manual backup of each server was queued; handleCreate
// checks, marks and queues under mu.
type cooldowns struct {
	mu   sync.Mutex
	last map[int64]time.Time
}

func newCooldowns() *cooldowns { return &cooldowns{last: map[int64]time.Time{}} }

// waitLocked returns how long server id must still wait at now (0 = it may start one); call
// with c.mu held.
func (c *cooldowns) waitLocked(id int64, now time.Time) time.Duration {
	last, ok := c.last[id]
	if !ok {
		return 0
	}
	return max(manualEvery-now.Sub(last), 0)
}

// clear forgets the mark t of server id (its backup failed); a newer mark stays.
func (c *cooldowns) clear(id int64, t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last[id].Equal(t) {
		delete(c.last, id)
	}
}
