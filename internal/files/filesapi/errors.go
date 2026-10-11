package filesapi

import (
	"errors"
	"net/http"
)

const (
	CodeNotFound            = "not_found"            // 404
	CodeExists              = "exists"               // 409
	CodeModified            = "modified"             // 409
	CodeNotRegular          = "not_regular"          // 400
	CodeNotText             = "not_text"             // 415
	CodeTooLarge            = "too_large"            // 413
	CodeInsufficientStorage = "insufficient_storage" // 507
	CodeBusy                = "busy"                 // 429
	CodeInvalid             = "invalid"              // 400
)

var (
	ErrNotFound            = errors.New(CodeNotFound)
	ErrExists              = errors.New(CodeExists)
	ErrModified            = errors.New(CodeModified)
	ErrNotRegular          = errors.New(CodeNotRegular)
	ErrNotText             = errors.New(CodeNotText)
	ErrTooLarge            = errors.New(CodeTooLarge)
	ErrInsufficientStorage = errors.New(CodeInsufficientStorage)
	ErrBusy                = errors.New(CodeBusy)
	ErrInvalid             = errors.New(CodeInvalid)
	// ErrUnavailable: the service is unreachable, broke the protocol or failed internally.
	ErrUnavailable = errors.New("files service unavailable")
)

type codeInfo struct {
	err    error
	status int
}

// codes is ordered: CodeOf returns the first sentinel an error matches.
var codes = []struct {
	code string
	codeInfo
}{
	{CodeNotFound, codeInfo{ErrNotFound, http.StatusNotFound}},
	{CodeExists, codeInfo{ErrExists, http.StatusConflict}},
	{CodeModified, codeInfo{ErrModified, http.StatusConflict}},
	{CodeNotRegular, codeInfo{ErrNotRegular, http.StatusBadRequest}},
	{CodeNotText, codeInfo{ErrNotText, http.StatusUnsupportedMediaType}},
	{CodeTooLarge, codeInfo{ErrTooLarge, http.StatusRequestEntityTooLarge}},
	{CodeInsufficientStorage, codeInfo{ErrInsufficientStorage, http.StatusInsufficientStorage}},
	{CodeBusy, codeInfo{ErrBusy, http.StatusTooManyRequests}},
	{CodeInvalid, codeInfo{ErrInvalid, http.StatusBadRequest}},
}

// ErrorFromCode returns the sentinel of a wire code (nil for unknown codes).
func ErrorFromCode(code string) error {
	for _, c := range codes {
		if c.code == code {
			return c.err
		}
	}
	return nil
}

// CodeOf maps err to its wire code and HTTP status (used by the service); errors without a
// sentinel are internal: "", 500.
func CodeOf(err error) (code string, status int) {
	for _, c := range codes {
		if errors.Is(err, c.err) {
			return c.code, c.status
		}
	}
	return "", http.StatusInternalServerError
}

// StatusError is a non-2xx answer of the service.
type StatusError struct {
	Status  int
	Code    string
	Message string
}

func (e *StatusError) Error() string {
	return "files: HTTP " + http.StatusText(e.Status) + ": " + e.Message
}

// Is maps a known code to its sentinel; unknown codes and answers without a code match
// ErrUnavailable.
func (e *StatusError) Is(target error) bool {
	if s := ErrorFromCode(e.Code); s != nil {
		return target == s
	}
	return target == ErrUnavailable
}
