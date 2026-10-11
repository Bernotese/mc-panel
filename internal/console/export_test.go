package console

// Timers lets the tests replace the stream timers (heartbeat, recheck, reconnect).
type Timers = timers

func SetTimers(h *Handler, t Timers) { h.timers = t }
