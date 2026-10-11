package filesapi

import "time"

// SetDownloadHeaderTimeout shortens the wait for download headers in tests.
func SetDownloadHeaderTimeout(c *UnixClient, d time.Duration) { c.downloadHeaderTimeout = d }
