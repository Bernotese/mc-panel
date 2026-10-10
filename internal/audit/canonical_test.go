package audit_test

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/store"
)

func TestCanonicalAndHashVector(t *testing.T) {
	row := store.AuditLog{
		ID: 1, Ts: 1760000000000, ActorType: "user", ActorID: sql.NullInt64{Int64: 7, Valid: true},
		ActorName: "jannes", Action: "user.create", Target: "user:8", Ip: "203.0.113.7", Result: "success",
		Details: `{"role":"user","username":"alice"}`,
		// PrevHash and Hash are not part of the canonical form.
		PrevHash: "ignored", Hash: "ignored",
	}
	wantCanon := `[1,1760000000000,"user",7,"jannes","user.create","user:8","203.0.113.7","success","{\"role\":\"user\",\"username\":\"alice\"}"]`
	if got := string(audit.Canonical(row)); got != wantCanon {
		t.Fatalf("Canonical =\n%s\nwant\n%s", got, wantCanon)
	}
	first := sha256.Sum256([]byte(wantCanon))
	if got := audit.ComputeHash("", row); got != hex.EncodeToString(first[:]) {
		t.Fatalf("ComputeHash(\"\") = %s", got)
	}
	prev := strings.Repeat("ab", 32)
	second := sha256.Sum256([]byte(prev + wantCanon))
	if got := audit.ComputeHash(prev, row); got != hex.EncodeToString(second[:]) || len(got) != 64 {
		t.Fatalf("ComputeHash(prev) = %s", got)
	}

	sys := row
	sys.ActorType, sys.ActorID, sys.ActorName = "system", sql.NullInt64{}, "system"
	if !strings.Contains(string(audit.Canonical(sys)), `,"system",null,"system",`) {
		t.Fatalf("NULL actor_id must encode as null: %s", audit.Canonical(sys))
	}
}

func TestCanonicalDetails(t *testing.T) {
	got, err := audit.CanonicalDetails(map[string]any{"b": map[string]any{"d": "x", "c": true}, "a": 1})
	if err != nil || got != `{"a":1,"b":{"c":true,"d":"x"}}` {
		t.Fatalf("CanonicalDetails = %q, %v", got, err)
	}
	if got, err := audit.CanonicalDetails(nil); err != nil || got != "{}" {
		t.Fatalf("nil details = %q, %v", got, err)
	}
	if _, err := audit.CanonicalDetails(map[string]any{"c": make(chan int)}); err == nil {
		t.Fatal("unencodable details must fail")
	}
}
