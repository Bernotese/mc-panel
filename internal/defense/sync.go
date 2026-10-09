package defense

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"mcpanel/internal/agent/agentapi"
	"mcpanel/internal/store"
)

// isUnavailable reports transient agent failures that must be retried later.
func isUnavailable(err error) bool {
	return errors.Is(err, agentapi.ErrUnavailable) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// maskedByAllow reports whether an active allow rule overlaps the jail's key.
// Such jails are enforced in the app only, never in the firewall.
func (s *Service) maskedByAllow(j store.Jail, now time.Time) bool {
	p, err := netip.ParsePrefix(j.IpKey)
	if err != nil {
		return false
	}
	return s.snap.Load().allowOverlaps(p, now)
}

func (s *Service) callAgent(ctx context.Context, fn func(context.Context) error) error {
	cctx, cancel := context.WithTimeout(ctx, AgentCallTimeout)
	defer cancel()
	return fn(cctx)
}

func msNull(t time.Time) sql.NullInt64 { return sql.NullInt64{Int64: t.UnixMilli(), Valid: true} }

// SyncOnce reconciles jails with the agent's firewall: unbans lifted jails,
// bans new jails and re-bans active jails missing from the firewall.
func (s *Service) SyncOnce(ctx context.Context) error {
	if s.agent == nil {
		return nil
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	now := s.clock.Now()
	if err := s.syncUnbans(ctx, now); err != nil {
		return err
	}
	if err := s.syncBans(ctx, now); err != nil {
		return err
	}
	return s.reconcile(ctx, now)
}

func (s *Service) syncUnbans(ctx context.Context, now time.Time) error {
	jails, err := s.q.DefenseJailsToUnban(ctx)
	if err != nil {
		return fmt.Errorf("defense: jails to unban: %w", err)
	}
	for _, j := range jails {
		p, perr := netip.ParsePrefix(j.IpKey)
		if perr != nil {
			s.log.Error("defense: invalid ip_key; marking unban as done", "jail_id", j.ID, "ip_key", j.IpKey)
		} else if err := s.callAgent(ctx, func(c context.Context) error { return s.agent.Unban(c, p) }); err != nil {
			if isUnavailable(err) {
				return fmt.Errorf("defense: unban %s: %w", p, err)
			}
			s.log.Error("defense: agent rejected unban; marking as done", "jail_id", j.ID, "prefix", p, "err", err)
		}
		if err := s.q.DefenseMarkJailUnbanned(ctx, store.DefenseMarkJailUnbannedParams{FwUnbannedAt: msNull(now), ID: j.ID}); err != nil {
			return fmt.Errorf("defense: mark unbanned: %w", err)
		}
	}
	return nil
}

func (s *Service) syncBans(ctx context.Context, now time.Time) error {
	jails, err := s.q.DefenseJailsToBan(ctx, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("defense: jails to ban: %w", err)
	}
	for _, j := range jails {
		if s.isUnbannable(j.ID) || s.maskedByAllow(j, now) {
			continue
		}
		ok, err := s.banJail(ctx, j, now)
		if err != nil {
			return err
		}
		if ok {
			if err := s.q.DefenseMarkJailBanned(ctx, store.DefenseMarkJailBannedParams{FwBannedAt: msNull(now), ID: j.ID}); err != nil {
				return fmt.Errorf("defense: mark banned: %w", err)
			}
		}
	}
	return nil
}

// banJail returns (true, nil) when the agent accepted the ban, (false, nil) when
// the jail was skipped or failed permanently, and an error when the agent is unavailable.
func (s *Service) banJail(ctx context.Context, j store.Jail, now time.Time) (bool, error) {
	p, err := netip.ParsePrefix(j.IpKey)
	if err == nil {
		err = agentapi.ValidateBanPrefix(p)
	}
	if err != nil {
		s.markUnbannable(j.ID)
		s.log.Error("defense: jail cannot be banned in the firewall", "jail_id", j.ID, "ip_key", j.IpKey, "err", err)
		return false, nil
	}
	remaining := time.UnixMilli(j.ExpiresAt).Sub(now)
	if remaining < time.Second {
		return false, nil
	}
	err = s.callAgent(ctx, func(c context.Context) error { return s.agent.Ban(c, p, remaining) })
	if err == nil {
		return true, nil
	}
	if isUnavailable(err) {
		return false, fmt.Errorf("defense: ban %s: %w", p, err)
	}
	s.markUnbannable(j.ID)
	s.log.Error("defense: agent rejected ban; not retrying", "jail_id", j.ID, "prefix", p, "err", err)
	return false, nil
}

// reconcile re-bans active jails whose ban is missing in the firewall
// (agent restart, host reboot).
func (s *Service) reconcile(ctx context.Context, now time.Time) error {
	jails, err := s.q.DefenseActiveBannedJails(ctx, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("defense: banned jails: %w", err)
	}
	if len(jails) == 0 {
		return nil
	}
	var bans []agentapi.Ban
	err = s.callAgent(ctx, func(c context.Context) error {
		var e error
		bans, e = s.agent.Bans(c)
		return e
	})
	if err != nil {
		if isUnavailable(err) {
			return fmt.Errorf("defense: list bans: %w", err)
		}
		s.log.Error("defense: agent rejected ban listing; skipping reconcile", "err", err)
		return nil
	}
	present := make(map[netip.Prefix]bool, len(bans))
	for _, b := range bans {
		if p, err := netip.ParsePrefix(b.Prefix); err == nil {
			present[p.Masked()] = true
		} else if a, err := netip.ParseAddr(b.Prefix); err == nil {
			// bare address: single-host prefix
			present[netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())] = true
		}
	}
	for _, j := range jails {
		p, err := netip.ParsePrefix(j.IpKey)
		if err != nil || s.isUnbannable(j.ID) {
			continue
		}
		if s.maskedByAllow(j, now) {
			// an allowlisted address lives inside this key: the app-level jail stays,
			// but the firewall must not cut off the allowlisted address
			if present[p.Masked()] {
				if err := s.callAgent(ctx, func(c context.Context) error { return s.agent.Unban(c, p) }); err != nil {
					if isUnavailable(err) {
						return fmt.Errorf("defense: unban %s: %w", p, err)
					}
					s.log.Error("defense: agent rejected unban of allowlisted jail", "jail_id", j.ID, "prefix", p, "err", err)
				}
			}
			continue
		}
		if present[p.Masked()] {
			continue
		}
		s.log.Warn("defense: ban missing in firewall; re-banning", "jail_id", j.ID, "prefix", p)
		ok, err := s.banJail(ctx, j, now)
		if err != nil {
			return err
		}
		if ok {
			if err := s.q.DefenseMarkJailBanned(ctx, store.DefenseMarkJailBannedParams{FwBannedAt: msNull(now), ID: j.ID}); err != nil {
				return fmt.Errorf("defense: mark banned: %w", err)
			}
		}
	}
	return nil
}

// PruneOnce deletes login attempts older than AttemptRetention.
func (s *Service) PruneOnce(ctx context.Context) (int64, error) {
	return s.q.DefenseDeleteAttemptsBefore(ctx, s.clock.Now().Add(-AttemptRetention).UnixMilli())
}

func (s *Service) cleanupLimiters(now time.Time) {
	for _, l := range []*keyedLimiter{s.loginIP, s.loginUser, s.secondFactor, s.api} {
		l.cleanup(now, LimiterIdle)
	}
}

// Run starts the background loops (firewall sync, rule refresh, retention,
// limiter cleanup) and blocks until ctx is cancelled.
func (s *Service) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.runSync(ctx)
	}()
	s.runMaintenance(ctx)
	wg.Wait()
}

func (s *Service) runSync(ctx context.Context) {
	t := time.NewTicker(SyncInterval)
	defer t.Stop()
	failing := false
	for {
		err := s.SyncOnce(ctx)
		switch {
		case err != nil && ctx.Err() != nil:
			return
		case err != nil && !failing:
			s.log.Warn("defense: firewall sync failed; will retry", "err", err)
			failing = true
		case err == nil && failing:
			s.log.Info("defense: firewall sync recovered")
			failing = false
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
		}
	}
}

func (s *Service) pruneOrLog(ctx context.Context) {
	if n, err := s.PruneOnce(ctx); err != nil {
		s.log.Error("defense: prune login attempts", "err", err)
	} else if n > 0 {
		s.log.Info("defense: pruned old login attempts", "count", n)
	}
}

func (s *Service) runMaintenance(ctx context.Context) {
	refresh := time.NewTicker(RulesRefreshInterval)
	prune := time.NewTicker(PruneInterval)
	limiters := time.NewTicker(LimiterCleanupInterval)
	defer refresh.Stop()
	defer prune.Stop()
	defer limiters.Stop()
	s.pruneOrLog(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
			s.reloadOrLog(ctx)
		case <-prune.C:
			s.pruneOrLog(ctx)
		case <-limiters.C:
			s.cleanupLimiters(s.clock.Now())
		}
	}
}
