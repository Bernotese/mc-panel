package serverfiles

import "time"

// SetIOTimeout shortens the stream silence timeout (deadline tests).
func SetIOTimeout(h *Handler, d time.Duration) { h.ioTimeout = d }

// MaxEditorCalls is the panel-wide limit of concurrent editor reads and saves.
const MaxEditorCalls = maxEditorCalls

// SetSaveTimeout shortens the overall bound of reading an editor save.
func SetSaveTimeout(h *Handler, d time.Duration) { h.saveTimeout = d }

// EditorSlotsInUse is the number of editor reads and saves running panel-wide.
func EditorSlotsInUse(h *Handler) int { return len(h.editor) }

// MaxEditorCallsPerUser is the per-user limit of concurrent editor reads and saves.
const MaxEditorCallsPerUser = maxEditorCallsPerUser
