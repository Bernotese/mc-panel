package auditapi

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Bernotese/mc-panel/internal/audit"
	"github.com/Bernotese/mc-panel/internal/clock"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/notify"
	"github.com/Bernotese/mc-panel/internal/rbac"
	"github.com/Bernotese/mc-panel/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 200
	maxFilterLen = 64
)

// Handler serves the read-only audit API. There is deliberately no update or delete endpoint.
type Handler struct {
	db       *sql.DB
	q        *store.Queries
	clock    clock.Clock
	notifier notify.Notifier
}

func NewHandler(db *sql.DB, c clock.Clock, n notify.Notifier) *Handler {
	return &Handler{db: db, q: store.New(db), clock: c, notifier: n}
}

// Routes registers the audit routes (paths relative to /api) with the rules from the OpenAPI table.
func (h *Handler) Routes(api *rbac.Router) {
	api.Get("/audit", rbac.Admin(), h.handleList)
	api.Get("/audit/verify", rbac.Admin(), h.handleVerify)
}

type entryJSON struct {
	ID        int64           `json:"id"`
	Ts        int64           `json:"ts"`
	ActorType string          `json:"actor_type"`
	ActorID   *int64          `json:"actor_id"`
	ActorName string          `json:"actor_name"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	IP        string          `json:"ip"`
	Result    string          `json:"result"`
	Details   json.RawMessage `json:"details"`
	Hash      string          `json:"hash"`
}

type pageJSON struct {
	Items      []entryJSON `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

type verifyJSON struct {
	OK         bool   `json:"ok"`
	Checked    int    `json:"checked"`
	FirstBadID *int64 `json:"first_bad_id"`
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	params, limit, msg := parseListQuery(r.URL.Query())
	if msg != "" {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeValidation, msg)
		return
	}
	rows, err := h.q.AuditList(r.Context(), params)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	page := pageJSON{Items: make([]entryJSON, 0, len(rows))}
	if len(rows) > limit {
		rows = rows[:limit]
		next := strconv.FormatInt(rows[limit-1].ID, 10)
		page.NextCursor = &next
	}
	for _, row := range rows {
		page.Items = append(page.Items, toEntryJSON(row))
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) handleVerify(w http.ResponseWriter, r *http.Request) {
	rep, err := audit.Verify(r.Context(), h.db)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := verifyJSON{OK: rep.OK, Checked: rep.Checked}
	if !rep.OK {
		id := rep.FirstBadID
		out.FirstBadID = &id
		h.notifier.Notify(r.Context(), notify.Event{
			Type:    notify.EventAuditChainBroken,
			Title:   "Audit chain broken",
			Message: fmt.Sprintf("The audit log hash chain is broken at entry %d (%d entries verified before it).", rep.FirstBadID, rep.Checked),
			Time:    h.clock.Now(),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func toEntryJSON(row store.AuditLog) entryJSON {
	e := entryJSON{
		ID: row.ID, Ts: row.Ts, ActorType: row.ActorType, ActorName: row.ActorName, Action: row.Action,
		Target: row.Target, IP: row.Ip, Result: row.Result, Details: json.RawMessage(row.Details), Hash: row.Hash,
	}
	if row.ActorID.Valid {
		id := row.ActorID.Int64
		e.ActorID = &id
	}
	if !json.Valid(e.Details) {
		// Only possible after manual tampering; keep the response well-formed.
		e.Details = json.RawMessage("{}")
	}
	return e
}

// globLiteral escapes SQLite GLOB metacharacters so s matches literally.
func globLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[':
			b.WriteByte('[')
			b.WriteRune(r)
			b.WriteByte(']')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// parseListQuery returns the query parameters, the page size and a validation message ("" = ok).
func parseListQuery(v url.Values) (store.AuditListParams, int, string) {
	actor, action, result := "*", "*", "*"
	if s := v.Get("actor"); s != "" {
		if len(s) > maxFilterLen {
			return store.AuditListParams{}, 0, fmt.Sprintf("actor must be at most %d characters", maxFilterLen)
		}
		actor = globLiteral(s)
	}
	if s := v.Get("action"); s != "" {
		if len(s) > maxFilterLen {
			return store.AuditListParams{}, 0, fmt.Sprintf("action must be at most %d characters", maxFilterLen)
		}
		action = globLiteral(s) + "*" // prefix match
	}
	if s := v.Get("result"); s != "" {
		switch audit.Result(s) {
		case audit.ResultSuccess, audit.ResultFailure, audit.ResultDenied:
			result = s
		default:
			return store.AuditListParams{}, 0, "result must be success, failure or denied"
		}
	}
	from, to, cursor := int64(math.MinInt64), int64(math.MaxInt64), int64(math.MaxInt64)
	for _, f := range []struct {
		name string
		dst  *int64
	}{{"from", &from}, {"to", &to}, {"cursor", &cursor}} {
		s := v.Get(f.name)
		if s == "" {
			continue
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return store.AuditListParams{}, 0, f.name + " must be an integer"
		}
		*f.dst = n
	}
	if v.Get("cursor") != "" && cursor <= 0 {
		return store.AuditListParams{}, 0, "cursor must be a positive integer"
	}
	limit := defaultLimit
	if s := v.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > maxLimit {
			return store.AuditListParams{}, 0, fmt.Sprintf("limit must be between 1 and %d", maxLimit)
		}
		limit = n
	}
	return store.AuditListParams{
		ActorGlob:  actor,
		ActionGlob: action,
		ResultGlob: result,
		FromTs:     from,
		ToTs:       to,
		BeforeID:   cursor,
		MaxRows:    int64(limit + 1),
	}, limit, ""
}
