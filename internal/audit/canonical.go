package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"mcpanel/internal/store"
)

// Canonical returns the exact bytes hashed for one audit row: the compact JSON array
//
//	[id, ts, actor_type, actor_id|null, actor_name, action, target, ip, result, details]
//
// encoded with encoding/json (default HTML escaping). details is the stored TEXT,
// embedded as a JSON string, so verification never re-encodes it.
func Canonical(row store.AuditLog) []byte {
	var actorID any
	if row.ActorID.Valid {
		actorID = row.ActorID.Int64
	}
	b, err := json.Marshal([]any{
		row.ID, row.Ts, row.ActorType, actorID, row.ActorName,
		row.Action, row.Target, row.Ip, row.Result, row.Details,
	})
	if err != nil {
		// Marshalling int64, string and nil values cannot fail.
		panic(fmt.Sprintf("audit: canonical: %v", err))
	}
	return b
}

// ComputeHash returns hex(SHA-256(prevHash || Canonical(row))).
func ComputeHash(prevHash string, row store.AuditLog) string {
	h := sha256.New()
	h.Write([]byte(prevHash))
	h.Write(Canonical(row))
	return hex.EncodeToString(h.Sum(nil))
}

// CanonicalDetails encodes details with encoding/json, which sorts map keys at every
// level. nil or empty details are stored as "{}".
func CanonicalDetails(d map[string]any) (string, error) {
	if len(d) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(d)
	if err != nil {
		return "", fmt.Errorf("audit: details: %w", err)
	}
	return string(b), nil
}
