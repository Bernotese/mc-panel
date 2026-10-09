// Package peercred authenticates Unix socket peers by their kernel-provided
// credentials (SO_PEERCRED on Linux, LOCAL_PEERCRED on macOS).
package peercred

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"slices"
)

// Conn is an accepted connection whose peer UID passed the allow check.
type Conn struct {
	net.Conn
	UID uint32
}

// PeerUID returns the effective UID of the process on the other end of c.
// c must be a *net.UnixConn.
func PeerUID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("peercred: %T is not a unix socket connection", c)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peercred: %w", err)
	}
	var (
		uid    uint32
		getErr error
	)
	if err := raw.Control(func(fd uintptr) {
		uid, getErr = peerUID(int(fd)) //nolint:gosec // G115: file descriptors fit into int
	}); err != nil {
		return 0, fmt.Errorf("peercred: %w", err)
	}
	if getErr != nil {
		return 0, fmt.Errorf("peercred: %w", getErr)
	}
	return uid, nil
}

// AllowUIDs returns an allow function accepting exactly the given UIDs.
func AllowUIDs(uids ...uint32) func(uid uint32) bool {
	allowed := slices.Clone(uids)
	return func(uid uint32) bool { return slices.Contains(allowed, uid) }
}

// Wrap returns a listener that only hands out connections whose peer UID is
// accepted by allow. Rejected connections are closed before any byte is read
// and Accept keeps waiting for the next connection.
func Wrap(l net.Listener, allow func(uid uint32) bool, log *slog.Logger) net.Listener {
	return &listener{Listener: l, allow: allow, log: log}
}

type listener struct {
	net.Listener
	allow func(uid uint32) bool
	log   *slog.Logger
}

func (l *listener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := PeerUID(c)
		if err != nil {
			l.log.Warn("agent: rejecting connection without peer credentials", "err", err)
			_ = c.Close()
			continue
		}
		if !l.allow(uid) {
			l.log.Warn("agent: rejecting connection from disallowed uid", "uid", uid)
			_ = c.Close()
			continue
		}
		return &Conn{Conn: c, UID: uid}, nil
	}
}

type ctxKey struct{}

// ConnContext stores the peer UID of a *Conn in the context. It has the
// signature of http.Server.ConnContext.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if pc, ok := c.(*Conn); ok {
		return context.WithValue(ctx, ctxKey{}, pc.UID)
	}
	return ctx
}

// FromContext returns the peer UID stored by ConnContext.
func FromContext(ctx context.Context) (uint32, bool) {
	uid, ok := ctx.Value(ctxKey{}).(uint32)
	return uid, ok
}
