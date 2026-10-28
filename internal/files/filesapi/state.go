package filesapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Data state of a server's data directory (data safety spec §2): the files service is the
// owner of this answer; the panel never infers "safe" from a missing one.
const (
	PathState           = "/v1/state"            // GET ?id= -> StateResponse; 503 when the journal cannot be read
	PathRecoveryResolve = "/v1/recovery/resolve" // POST IDRequest -> Recovery; 404 unless recovery_required
)

// ResolveTimeout bounds the panel's resolve call: the service first waits up to 30 s for the
// server's mutation leases to drain (data safety spec §2.1).
const ResolveTimeout = 45 * time.Second

type DataState string

const (
	// StateSafe: no job rewrites the directory and no restore journal is unresolved.
	StateSafe DataState = "safe"
	// StateBusy: a restore or extract holds the directory.
	StateBusy DataState = "busy"
	// StateRecoveryRequired: a restore journal is stuck, unreadable or could not be recovered.
	StateRecoveryRequired DataState = "recovery_required"
)

func (s DataState) Valid() bool {
	switch s {
	case StateSafe, StateBusy, StateRecoveryRequired:
		return true
	}
	return false
}

// Recovery describes an unresolved restore of a server (StateRecoveryRequired) or, after a
// resolve, where its half-swapped folders were moved to.
type Recovery struct {
	Phase  string `json:"phase"`            // journal phase, or "unreadable"
	Since  int64  `json:"since"`            // Unix ms of the journal entry; 0 = unknown
	Folder string `json:"folder,omitempty"` // after a resolve: the folder below backups/recovery/
}

type StateResponse struct {
	State    DataState `json:"state"`
	Job      *Job      `json:"job"`      // the server's current or last job; nil = none
	Recovery *Recovery `json:"recovery"` // set exactly when State is StateRecoveryRequired
}

// StateClient is the panel-side view of the data state endpoints.
type StateClient interface {
	State(ctx context.Context, id int64) (StateResponse, error)
	ResolveRecovery(ctx context.Context, id int64) (Recovery, error)
}

var _ StateClient = (*UnixClient)(nil)

// State asks for the data state of server id. Any error (also a 503 for an unreadable journal)
// means the state is unknown to the caller.
func (c *UnixClient) State(ctx context.Context, id int64) (StateResponse, error) {
	if err := checkID(id); err != nil {
		return StateResponse{}, err
	}
	var out StateResponse
	q := url.Values{"id": {strconv.FormatInt(id, 10)}}
	if err := c.call(ctx, c.callTimeout, http.MethodGet, PathState+"?"+q.Encode(), nil, &out); err != nil {
		return StateResponse{}, err
	}
	if !out.State.Valid() {
		return StateResponse{}, ErrUnavailable
	}
	return out, nil
}

// ResolveRecovery moves the half-swapped folders of an unresolved restore of server id out of
// its data directory and removes the journal entry (data safety spec §2.1).
func (c *UnixClient) ResolveRecovery(ctx context.Context, id int64) (Recovery, error) {
	if err := checkID(id); err != nil {
		return Recovery{}, err
	}
	var out Recovery
	err := c.call(ctx, ResolveTimeout, http.MethodPost, PathRecoveryResolve, IDRequest{ID: id}, &out)
	return out, err
}
