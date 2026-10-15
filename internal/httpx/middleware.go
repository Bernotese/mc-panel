package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
)

func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id := hex.EncodeToString(b)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// CSP is the panel's Content-Security-Policy. styleNonce, if set, allows the inline styles
// that carry it: the SPA's index.html gets a fresh one per response (internal/webui), because
// Radix, CodeMirror and Vite insert <style> elements at runtime. Every other response gets
// CSP(""). web/vite.config.ts sends the same policy from the dev server (checked by a test).
func CSP(styleNonce string) string {
	styleSrc := "'self'"
	if styleNonce != "" {
		styleSrc += " 'nonce-" + styleNonce + "'"
	}
	return "default-src 'self'; script-src 'self'; style-src " + styleSrc + "; " +
		"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}

// CSPReportOnly reports (but does not block) DOM sinks that would need Trusted Types; the
// e2e suite fails on any report, so the policy can be enforced once it stays clean.
const CSPReportOnly = "require-trusted-types-for 'script'; trusted-types 'none'"

var staticCSP = CSP("")

// SecurityHeaders sets the panel's own security headers (independent of the reverse proxy).
// Outside dev mode it also sends HSTS: production is always served over https.
func SecurityHeaders(dev bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("Content-Security-Policy", staticCSP)
			h.Set("Content-Security-Policy-Report-Only", CSPReportOnly)
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			h.Set("X-Frame-Options", "DENY")
			if !dev {
				h.Set("Strict-Transport-Security", "max-age=31536000")
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				h.Set("Cache-Control", "no-store")
			}
			next.ServeHTTP(w, r)
		})
	}
}

func BodyLimit(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, n)
			next.ServeHTTP(w, r)
		})
	}
}

// BodyLimitExcept is BodyLimit for every request except those skip accepts. A streaming
// endpoint (the file upload, spec 3b §5.1) enforces its own limit downstream.
func BodyLimitExcept(n int64, skip func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !skip(r) {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}
