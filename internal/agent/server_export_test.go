package agent

import "time"

// WithReadTimeout overrides the server's ReadTimeout (tests of long-running requests).
func WithReadTimeout(d time.Duration) Option { return func(s *Server) { s.readTimeout = d } }
