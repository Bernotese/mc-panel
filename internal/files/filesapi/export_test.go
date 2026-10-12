package filesapi

import "time"

// SetDownloadHeaderTimeout shortens the wait for download headers in tests.
func SetDownloadHeaderTimeout(c *UnixClient, d time.Duration) { c.downloadHeaderTimeout = d }

// SetCallTimeouts sets the timeout of the small calls and the long one of Delete and Extract.
func SetCallTimeouts(c *UnixClient, call, long time.Duration) {
	c.callTimeout, c.deleteTimeout, c.extractTimeout = call, long, long
}

// SetJarsTimeout sets the timeout of a jars listing.
func SetJarsTimeout(c *UnixClient, d time.Duration) { c.jarsTimeout = d }
