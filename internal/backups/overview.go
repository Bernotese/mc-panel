package backups

import (
	"context"
	"net/http"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
)

// snapshotView is the OpenAPI schema BackupSnapshot.
type snapshotView struct {
	ID      string  `json:"id"`
	ShortID string  `json:"short_id"`
	Time    int64   `json:"time"`
	Kind    string  `json:"kind"`
	Pinned  bool    `json:"pinned"`
	Note    *string `json:"note"`
	Bytes   int64   `json:"bytes"`
}

func viewSnapshot(s filesapi.Snapshot) snapshotView {
	v := snapshotView{ID: s.ID, ShortID: s.ShortID, Time: s.Time, Kind: string(s.Kind), Pinned: s.Pinned, Bytes: s.Bytes}
	if s.Note != "" {
		n := s.Note
		v.Note = &n
	}
	return v
}

type overviewResponse struct {
	Available bool           `json:"available"`
	Snapshots []snapshotView `json:"snapshots"`
	Job       *jobView       `json:"job"`
	Schedule  scheduleView   `json:"schedule"`
}

type snapshotResponse struct {
	Snapshot snapshotView `json:"snapshot"`
}

// findSnapshot looks id up in the server's repository.
func (h *Handler) findSnapshot(ctx context.Context, serverID int64, id string) (filesapi.Snapshot, error) {
	list, err := h.files.Snapshots(ctx, serverID)
	if err != nil {
		return filesapi.Snapshot{}, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
	}
	return filesapi.Snapshot{}, errSnapshotNotFound
}

// handleOverview never fails for a missing service or restic: the tab shows its hint (V3).
func (h *Handler) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	srv, ok := h.loadServer(w, r)
	if !ok {
		return
	}
	row, err := h.loadSchedule(ctx, srv.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	_, tz, err := h.location(ctx)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := overviewResponse{Snapshots: []snapshotView{}, Job: h.queue.get(srv.ID), Schedule: viewSchedule(row, tz)}
	if h.ready(ctx, srv, false) == nil {
		list, err := h.files.Snapshots(ctx, srv.ID)
		if err != nil {
			h.log.WarnContext(ctx, "backups: list snapshots", "server_id", srv.ID, "err", err)
		} else {
			out.Available = true
			for _, s := range list {
				out.Snapshots = append(out.Snapshots, viewSnapshot(s))
			}
		}
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
