package defense

import (
	"context"
	"database/sql"
	"net/netip"
	"testing"
	"time"

	"mcpanel/internal/audit"
	"mcpanel/internal/clock"
	"mcpanel/internal/notify"
	"mcpanel/internal/store"
	"mcpanel/internal/store/storetest"
)

var t0 = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

// fakeGeo maps normalized IP strings to country codes.
type fakeGeo map[string]string

func (g fakeGeo) Country(ip netip.Addr) string { return g[normalizeIP(ip).String()] }

type env struct {
	db      *sql.DB
	q       *store.Queries
	clk     *clock.Fake
	audit   *audit.MemoryRecorder
	notes   *notify.Recording
	geo     fakeGeo
	svc     *Service
	adminID int64
}

func newEnv(t *testing.T, opts ...func(*Deps)) *env {
	t.Helper()
	db := storetest.Open(t)
	e := &env{
		db: db, q: store.New(db), clk: clock.NewFake(t0),
		audit: &audit.MemoryRecorder{}, notes: &notify.Recording{}, geo: fakeGeo{},
	}
	e.adminID = storetest.CreateUser(t, db, "admin", "admin")
	d := Deps{DB: db, Clock: e.clk, Geo: e.geo, GeoAvailable: true, Audit: e.audit, Notifier: e.notes}
	for _, o := range opts {
		o(&d)
	}
	svc, err := NewService(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	e.svc = svc
	return e
}

func (e *env) reload(t *testing.T) {
	t.Helper()
	if err := e.svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *env) addRule(t *testing.T, cidr, kind string, expires *time.Time) int64 {
	t.Helper()
	var exp sql.NullInt64
	if expires != nil {
		exp = sql.NullInt64{Int64: expires.UnixMilli(), Valid: true}
	}
	r, err := e.q.DefenseInsertIPRule(context.Background(), store.DefenseInsertIPRuleParams{
		Cidr: cidr, Kind: kind, Comment: "", ExpiresAt: exp, CreatedBy: "test", CreatedAt: e.clk.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.reload(t)
	return r.ID
}

func (e *env) saveSettings(t *testing.T, s store.SecuritySettings) {
	t.Helper()
	if err := store.SaveSecuritySettings(context.Background(), e.q, s, e.clk.Now()); err != nil {
		t.Fatal(err)
	}
	e.reload(t)
}

func (e *env) insertJail(t *testing.T, key string, created, expires time.Time) int64 {
	t.Helper()
	j, err := e.q.DefenseInsertJail(context.Background(), store.DefenseInsertJailParams{
		IpKey: key, Reason: "test", Level: 1, Manual: 0, CreatedAt: created.UnixMilli(), ExpiresAt: expires.UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return j.ID
}

func addr(s string) netip.Addr { return netip.MustParseAddr(s) }
