package files

import (
	"net/http"
	"time"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// backupRoutes are the endpoints of spec 4 §4.1; NewServer adds them to its routes.
func (s *Server) backupRoutes() map[string]route {
	return map[string]route{
		filesapi.PathBackupsStatus:   {method: http.MethodGet, handler: s.backupsStatus},
		filesapi.PathBackupsList:     {method: http.MethodPost, handler: s.backupsList},
		filesapi.PathBackupsCreate:   {method: http.MethodPost, handler: s.backupsCreate},
		filesapi.PathBackupsRestore:  {method: http.MethodPost, handler: s.backupsRestore},
		filesapi.PathJobsCancel:      {method: http.MethodPost, handler: s.cancelJob},
		filesapi.PathBackupsForget:   {method: http.MethodPost, handler: s.backupsForget},
		filesapi.PathBackupsPin:      {method: http.MethodPost, handler: s.backupsPin},
		filesapi.PathBackupsPurge:    {method: http.MethodPost, handler: s.backupsPurge},
		filesapi.PathBackupsDownload: {method: http.MethodGet, handler: s.backupsDownload},
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

// backupsRestore: the pre-check lists the snapshots (restic reads the index): longOp.
func (s *Server) backupsRestore(w http.ResponseWriter, r *http.Request) {
	var req filesapi.SnapshotRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var job filesapi.Job
	err := s.longOp(w, "backups.restore", func() error {
		var err error
		job, err = s.svc.RestoreBackup(r.Context(), req)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.restore", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	var req filesapi.IDRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.svc.CancelJob(req.ID); err != nil {
		s.fail(w, r, "jobs.cancel", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) backupsForget(w http.ResponseWriter, r *http.Request) {
	var req filesapi.ForgetRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var job filesapi.Job
	err := s.longOp(w, "backups.forget", func() error {
		var err error
		job, err = s.svc.ForgetBackups(r.Context(), req)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.forget", err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) backupsPin(w http.ResponseWriter, r *http.Request) {
	var req filesapi.PinRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	var sn filesapi.Snapshot
	err := s.longOp(w, "backups.pin", func() error {
		var err error
		sn, err = s.svc.PinBackup(r.Context(), req)
		return err
	})
	if err != nil {
		s.fail(w, r, "backups.pin", err)
		return
	}
	writeJSON(w, http.StatusOK, filesapi.SnapshotResponse{Snapshot: sn})
}

func (s *Server) backupsPurge(w http.ResponseWriter, r *http.Request) {
	var req filesapi.IDRequest
	if !s.decode(w, r, &req, filesapi.MaxSmallJSONBytes) {
		return
	}
	if err := s.longOp(w, "backups.purge", func() error { return s.svc.PurgeBackups(r.Context(), req.ID) }); err != nil {
		s.fail(w, r, "backups.purge", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// backupsDownload: GET ?id=&snapshot=, a zip stream like the folder download (errors after the
// first byte abort the response).
func (s *Server) backupsDownload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	id, err := parseID(q.Get("id"))
	if err != nil {
		s.fail(w, r, "backups.download", err)
		return
	}
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{}) // the pre-check lists the snapshots
	started := false
	start := func() {
		started = true
		h := w.Header()
		h.Set(filesapi.HeaderKind, "dir")
		h.Set("Content-Type", "application/zip")
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()
	}
	err = s.svc.DownloadBackup(r.Context(), id, q.Get("snapshot"), start, &deadlineWriter{w: w, rc: rc, now: s.io.Now})
	if err == nil {
		return
	}
	if !started {
		_ = rc.SetWriteDeadline(s.io.Now().Add(ioTimeout))
		s.fail(w, r, "backups.download", err)
		return
	}
	if r.Context().Err() == nil {
		s.log.Warn("files: backup download aborted", "id", id, "err", err)
	}
	panic(http.ErrAbortHandler)
}
