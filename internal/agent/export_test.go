package agent

import "time"

// SetLogsHeaderTimeout shortens the wait for the logs response headers in tests.
func SetLogsHeaderTimeout(c *Client, d time.Duration) { c.logsHeaderTimeout = d }
