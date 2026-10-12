package files

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// backupRoutes are the endpoints of spec 4 §4.1; NewServer adds them to its routes.
func (s *Server) backupRoutes() map[string]route {
	return map[string]route{
		filesapi.PathBackupsStatus: {method: http.MethodGet, handler: s.backupsStatus},
		filesapi.PathBackupsList:   {method: http.MethodPost, handler: s.backupsList},
		filesapi.PathBackupsCreate: {method: http.MethodPost, handler: s.backupsCreate},
	}
}

// backupsStatus always answers, also without restic (spec 4 §3.2).
func (s *Server) backupsStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.BackupsStatus())
}

// backupsList: restic reads the repository index, which can outlast the server's WriteTimeout
// (the client waits filesapi.BackupCallTimeout): longOp.
func (s *Server) backupsList(w http.ResponseWriter, r *http.Request) {
	var req filesapi.IDRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var list []filesapi.Snapshot
	err := s.longOp(w, "backups.list", func() error {
		var err error
		list, err = s.svc.Snapshots(r.Context(), req.ID)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.list", err)
		return
	}
	writeJSON(w, http.StatusOK, filesapi.SnapshotsResponse{Snapshots: list})
}

func (s *Server) backupsCreate(w http.ResponseWriter, r *http.Request) {
	var req filesapi.CreateBackupRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	job, err := s.svc.CreateBackup(r.Context(), req)
	if err != nil {
		s.fail(w, r, "backups.create", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}
