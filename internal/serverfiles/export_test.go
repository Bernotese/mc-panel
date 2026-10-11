package serverfiles

import "time"

// SetIOTimeout shortens the stream silence timeout (deadline tests).
func SetIOTimeout(h *Handler, d time.Duration) { h.ioTimeout = d }

// MaxEditorCalls is the panel-wide limit of concurrent editor reads and saves.
const MaxEditorCalls = maxEditorCalls
