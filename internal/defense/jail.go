package defense

import (
	"context"
	"database/sql"
	"fmt"
	"net/netip"
	"time"
	"unicode/utf8"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/store"
)

var _ LoginGuard = (*Service)(nil)

const (
	maxUsernameLen  = 64
	maxUserAgentLen = 256
	maxReasonLen    = 64
)

// ReasonComplete marks a fully successful login (Stream A contract).
const ReasonComplete = "complete"

// JailDuration returns the jail duration for an escalation level (spec §7.2).
func JailDuration(level int64) time.Duration {
	if level < 1 {
		level = 1
	}
	if level > 10 {
		return JailMax
	}
	d := JailBase << uint(level-1)
	if d > JailMax {
		return JailMax
	}
	return d
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func countryOrUnknown(c string) string {
	if c == "" {
		return "unknown country"
	}
	return c
}

// RecordAttempt stores the attempt, notifies about logins from new devices and
// jails the IP key when the failure threshold is reached.
func (s *Service) RecordAttempt(ctx context.Context, a Attempt) error {
	now := s.clock.Now()
	ip := normalizeIP(a.IP)
	key := httpx.IPKey(ip)
	if !key.IsValid() {
		return fmt.Errorf("defense: record attempt: invalid ip %v", a.IP)
	}
	var uid sql.NullInt64
	if a.UserID != nil {
		uid = sql.NullInt64{Int64: *a.UserID, Valid: true}
	}
	newDevice := false
	if a.Success && a.Reason == ReasonComplete && uid.Valid {
		n, err := s.q.DefenseCountFullLoginsFrom(ctx, store.DefenseCountFullLoginsFromParams{UserID: uid, IpKey: key.String()})
		if err != nil {
			return fmt.Errorf("defense: check new device: %w", err)
		}
		newDevice = n == 0
	}
	country := s.geo.Country(ip)
	var net56, net48 string
	if ip.Is6() {
		net56, net48 = netPrefix(ip, 56).String(), netPrefix(ip, 48).String()
	}
	err := s.q.DefenseInsertAttempt(ctx, store.DefenseInsertAttemptParams{
		Ts:        now.UnixMilli(),
		Ip:        ip.String(),
		IpKey:     key.String(),
		Username:  truncateRunes(a.Username, maxUsernameLen),
		UserID:    uid,
		Stage:     string(a.Stage),
		Success:   boolInt(a.Success),
		Reason:    truncateRunes(a.Reason, maxReasonLen),
		UserAgent: truncateRunes(a.UserAgent, maxUserAgentLen),
		Country:   country,
		Net56:     net56,
		Net48:     net48,
	})
	if err != nil {
		return fmt.Errorf("defense: record attempt: %w", err)
	}
	if !a.Success && a.Stage == StagePassword && !s.snap.Load().allowed(ip, now) {
		// the per-username bucket counts failed password attempts only (see CheckLogin)
		s.loginUser.consume(usernameKey(a.Username), now)
	}
	if a.Success {
		if newDevice {
			s.notifier.Notify(ctx, notify.Event{
				Type:     notify.EventNewDeviceLogin,
				Title:    "Login from a new device",
				Message:  fmt.Sprintf("%s signed in from %s (%s).", a.Username, ip, countryOrUnknown(country)),
				IP:       key.String(),
				Username: a.Username,
				Time:     now,
			})
		}
		return nil
	}
	return s.maybeJail(ctx, ip, key, now)
}

// jailLevel is one prefix under which failures are counted and jailed.
type jailLevel struct {
	key       netip.Prefix
	threshold int64
	count     func(ctx context.Context, key string, since int64) (int64, error)
}

// jailLevels lists the prefixes of ip from the widest to the narrowest: an IPv6 address is
// also counted per /56 and /48 (SEC-09), so rotating /64s of one prefix does not help.
func (s *Service) jailLevels(ip netip.Addr, key netip.Prefix) []jailLevel {
	byKey := func(ctx context.Context, k string, since int64) (int64, error) {
		return s.q.DefenseCountFailuresSince(ctx, store.DefenseCountFailuresSinceParams{IpKey: k, Since: since})
	}
	levels := []jailLevel{{key: key, threshold: FailureThreshold, count: byKey}}
	if !ip.Is6() {
		return levels
	}
	by48 := func(ctx context.Context, k string, since int64) (int64, error) {
		return s.q.DefenseCountFailuresInNet48Since(ctx, store.DefenseCountFailuresInNet48SinceParams{Net: k, Since: since})
	}
	by56 := func(ctx context.Context, k string, since int64) (int64, error) {
		return s.q.DefenseCountFailuresInNet56Since(ctx, store.DefenseCountFailuresInNet56SinceParams{Net: k, Since: since})
	}
	return append([]jailLevel{
		{key: netPrefix(ip, 48), threshold: Net48FailureThreshold, count: by48},
		{key: netPrefix(ip, 56), threshold: Net56FailureThreshold, count: by56},
	}, levels...)
}

// maybeJail jails ip's key, or for IPv6 its /56 or /48, when one of them reached its failure
// threshold. The widest prefix wins: once it is jailed, narrower jails would add nothing.
// Allowlisted and loopback addresses are never jailed.
func (s *Service) maybeJail(ctx context.Context, ip netip.Addr, key netip.Prefix, now time.Time) error {
	if ip.IsLoopback() || s.snap.Load().allowed(ip, now) {
		return nil
	}
	s.jailMu.Lock()
	defer s.jailMu.Unlock()
	for _, l := range s.jailLevels(ip, key) {
		done, err := s.jailIfOver(ctx, ip, l, now)
		if err != nil || done {
			return err
		}
	}
	return nil
}

// jailIfOver jails l.key if it has at least l.threshold failures in the window (counted after
// its last jail). done is true when l.key is jailed afterwards (also if it already was).
func (s *Service) jailIfOver(ctx context.Context, ip netip.Addr, l jailLevel, now time.Time) (done bool, err error) {
	key := l.key
	if _, ok := s.snap.Load().jailedUntil(key, now); ok {
		return true, nil
	}
	last, err := s.q.DefenseLastJailCreatedAt(ctx, key.String())
	if err != nil {
		return false, fmt.Errorf("defense: last jail: %w", err)
	}
	since := now.Add(-FailureWindow).UnixMilli()
	if last >= since {
		since = last + 1
	}
	failures, err := l.count(ctx, key.String(), since)
	if err != nil {
		return false, fmt.Errorf("defense: count failures: %w", err)
	}
	if failures < l.threshold {
		return false, nil
	}
	prior, err := s.q.DefenseCountJailsSince(ctx, store.DefenseCountJailsSinceParams{
		IpKey: key.String(), Since: now.Add(-RepeatWindow).UnixMilli(),
	})
	if err != nil {
		return false, fmt.Errorf("defense: count jails: %w", err)
	}
	level := prior + 1
	expires := now.Add(JailDuration(level))
	j, err := s.q.DefenseInsertJail(ctx, store.DefenseInsertJailParams{
		IpKey:     key.String(),
		Reason:    fmt.Sprintf("%d failed login attempts within %s", failures, FailureWindow),
		Level:     level,
		Manual:    0,
		CreatedAt: now.UnixMilli(),
		ExpiresAt: expires.UnixMilli(),
	})
	if err != nil {
		return false, fmt.Errorf("defense: insert jail: %w", err)
	}
	s.addJail(key, expires)
	s.kickSync()
	if err := s.audit.Record(ctx, audit.Entry{
		Action:    audit.ActionJailAuto,
		Target:    key.String(),
		Result:    audit.ResultSuccess,
		ActorType: audit.ActorSystem,
		ActorName: "system",
		IP:        ip.String(),
		Details:   map[string]any{"jail_id": j.ID, "level": level, "failures": failures, "expires_at": expires.UnixMilli()},
	}); err != nil {
		s.log.Error("defense: audit jail", "err", err)
	}
	s.notifier.Notify(ctx, notify.Event{
		Type:    notify.EventJailed,
		Title:   "IP jailed",
		Message: fmt.Sprintf("%s was jailed for %s after %d failed login attempts (level %d).", key, JailDuration(level), failures, level),
		IP:      key.String(),
		Time:    now,
	})
	return true, nil
}
