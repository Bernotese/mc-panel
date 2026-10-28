// Package httpx contains shared HTTP helpers: error format, JSON I/O, client IP and middleware.
package httpx

import (
	"log/slog"
	"net/http"
)

// Error codes. Handlers must only use these.
const (
	CodeBadRequest              = "bad_request"
	CodeValidation              = "validation_failed"
	CodeUnauthorized            = "unauthorized"
	CodeInvalidCredentials      = "invalid_credentials"
	CodeInvalidCode             = "invalid_code"
	CodeForbidden               = "forbidden"
	CodeSudoRequired            = "sudo_required"
	CodeSetupRequired           = "setup_2fa_required"
	CodePasswordChangeRequired  = "password_change_required"
	CodeCSRF                    = "csrf_invalid"
	CodeNotFound                = "not_found"
	CodeConflict                = "conflict"
	CodePayloadTooLarge         = "payload_too_large"
	CodeRateLimited             = "rate_limited"
	CodeJailed                  = "jailed"
	CodeIPBlocked               = "ip_blocked"
	CodeAgentUnavailable        = "agent_unavailable"
	CodeDockerUnavailable       = "docker_unavailable"
	CodeBaseDomainMissing       = "base_domain_missing"
	CodeCatalogUnavailable      = "catalog_unavailable"
	CodeInstanceNotRunning      = "instance_not_running"
	CodeRconUnavailable         = "rcon_unavailable"
	CodeFilesUnavailable        = "files_unavailable"
	CodeFileExists              = "file_exists"
	CodeFileModified            = "file_modified"
	CodeNotText                 = "not_text"
	CodeNotRegular              = "not_regular"
	CodeInstanceRunning         = "instance_running"
	CodeInsufficientStorage     = "insufficient_storage"
	CodePluginsUnsupported      = "plugins_unsupported"
	CodePluginSourceUnavailable = "plugin_source_unavailable"
	CodePluginNotDownloadable   = "plugin_not_downloadable"
	CodePluginIncompatible      = "plugin_incompatible"
	CodeMrpackIncompatible      = "mrpack_incompatible"
	CodeMrpackVersionMismatch   = "mrpack_version_mismatch"
	CodeBackupsUnavailable      = "backups_unavailable"
	CodeBackupRunning           = "backup_running"
	CodeDevExists               = "dev_exists"
	CodeNotDev                  = "not_dev"
	CodeDevManaged              = "dev_managed"
	CodeServerModified          = "server_modified"
	CodeInternal                = "internal"
)

type ErrorBody struct {
	Error     string `json:"error"`
	Message   string `json:"message"`
	RequestID string `json:"request_id,omitempty"`
}

func WriteError(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	body := ErrorBody{Error: code, Message: msg}
	if status >= 500 {
		body.RequestID = RequestIDFrom(r.Context())
	}
	WriteJSON(w, status, body)
}

// Internal logs err (never sent to the client) and writes a generic 500.
func Internal(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal error",
		"err", err, "request_id", RequestIDFrom(r.Context()), "method", r.Method, "path", r.URL.Path)
	WriteError(w, r, http.StatusInternalServerError, CodeInternal, "internal server error")
}
