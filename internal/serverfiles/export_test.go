package serverfiles

import "time"

// SetIOTimeout shortens the stream silence timeout (deadline tests).
func SetIOTimeout(h *Handler, d time.Duration) { h.ioTimeout = d }
