package httpx

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
)

type ctxKey int

const (
	clientIPKey ctxKey = iota
	requestIDKey
)

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
// honoured when the direct peer is a trusted proxy; the rightmost untrusted
// entry is used so clients cannot spoof by prepending addresses.
func RealIP(trusted []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
// rejects it) instead of attributing the request to the proxy.
func fromXFF(header string, trusted []netip.Prefix, fallback netip.Addr) netip.Addr {
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
		if err != nil {
			return netip.Addr{}
		}
		a = a.Unmap().WithZone("")
		if !isTrusted(a, trusted) {
			return a
		}
	}
	return fallback
}

func isTrusted(a netip.Addr, trusted []netip.Prefix) bool {
	for _, p := range trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
