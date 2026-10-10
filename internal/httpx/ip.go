package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type ctxKey int

const (
	clientIPKey ctxKey = iota
	requestIDKey
	unixPeerKey
)

// UnixConnContext is an http.Server ConnContext hook: it marks connections accepted on a unix
// socket. In production the panel listens on a unix socket that only the reverse proxy can
// reach, so such a peer is treated as a trusted proxy by RealIP (its RemoteAddr is "@" or "",
// which cannot be matched against trusted_proxies).
func UnixConnContext(ctx context.Context, c net.Conn) context.Context {
	if _, ok := c.(*net.UnixConn); ok {
		return context.WithValue(ctx, unixPeerKey, true)
	}
	return ctx
}

func isUnixPeer(ctx context.Context) bool {
	v, _ := ctx.Value(unixPeerKey).(bool)
	return v
}

func ClientIP(ctx context.Context) netip.Addr {
	ip, _ := ctx.Value(clientIPKey).(netip.Addr)
	return ip
}

func WithClientIP(ctx context.Context, ip netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey, ip)
}

// IPKey returns the key under which rate limits and jails are tracked:
// IPv4 addresses as /32, IPv6 addresses as their /64 network (clients usually
// control a whole /64 and could otherwise rotate addresses).
func IPKey(a netip.Addr) netip.Prefix {
	if !a.IsValid() {
		return netip.Prefix{}
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return netip.PrefixFrom(a, 32)
	}
	p, _ := a.Prefix(64)
	return p
}

// RealIP stores the client IP in the request context. X-Forwarded-For is only
// honoured when the direct peer is a trusted proxy (a trusted_proxies address, or any peer on
// the unix socket, see UnixConnContext); the rightmost untrusted entry is used so clients
// cannot spoof by prepending addresses. Over the unix socket a request without a usable
// X-Forwarded-For gets no client IP at all (the IP filter rejects it: fail closed).
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isUnixPeer(r.Context()) {
				var ip netip.Addr
				if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
					ip = fromXFF(strings.Join(xff, ","), trusted, netip.Addr{})
				}
				next.ServeHTTP(w, r.WithContext(WithClientIP(r.Context(), ip)))
				return
			}
			ip := peerIP(r.RemoteAddr)
			if ip.IsValid() && isTrusted(ip, trusted) {
				if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
					ip = fromXFF(strings.Join(xff, ","), trusted, ip)
				}
			}
			next.ServeHTTP(w, r.WithContext(WithClientIP(r.Context(), ip)))
		})
	}
}

func peerIP(remote string) netip.Addr {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().Unmap().WithZone("")
	}
	if a, err := netip.ParseAddr(remote); err == nil {
		return a.Unmap().WithZone("")
	}
	return netip.Addr{}
}

// fromXFF walks the header from the right and returns the first untrusted entry. An
// unparsable entry reached before that yields an invalid address (fail closed: the IP filter
// rejects it) instead of attributing the request to the proxy. If every entry is trusted,
// fallback is returned (the TCP peer); an invalid fallback (unix peer) selects the leftmost
// entry, i.e. the client as seen by the first proxy.
func fromXFF(header string, trusted []netip.Prefix, fallback netip.Addr) netip.Addr {
	parts := strings.Split(header, ",")
	var a netip.Addr
	for i := len(parts) - 1; i >= 0; i-- {
		parsed, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return netip.Addr{}
		}
		a = parsed.Unmap().WithZone("")
		if !isTrusted(a, trusted) {
			return a
		}
	}
	if fallback.IsValid() {
		return fallback
	}
	return a
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
