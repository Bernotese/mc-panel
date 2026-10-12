package files

import (
	"net/http"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

// backupRoutes are the endpoints of spec 4 §4.1; NewServer adds them to its routes.
func (s *Server) backupRoutes() map[string]route {
	return map[string]route{
		filesapi.PathBackupsStatus: {method: http.MethodGet, handler: s.backupsStatus},
	}
}

// backupsStatus always answers, also without restic (spec 4 §3.2).
func (s *Server) backupsStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.svc.BackupsStatus())
}
