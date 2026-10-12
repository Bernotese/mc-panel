package filesapi_test

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
)

func TestCodesRoundTrip(t *testing.T) {
	for code, status := range map[string]int{
		filesapi.CodeNotFound:            http.StatusNotFound,
		filesapi.CodeExists:              http.StatusConflict,
		filesapi.CodeModified:            http.StatusConflict,
		filesapi.CodeNotRegular:          http.StatusBadRequest,
		filesapi.CodeNotText:             http.StatusUnsupportedMediaType,
		filesapi.CodeTooLarge:            http.StatusRequestEntityTooLarge,
		filesapi.CodeInsufficientStorage: http.StatusInsufficientStorage,
		filesapi.CodeBusy:                http.StatusTooManyRequests,
		filesapi.CodeInvalid:             http.StatusBadRequest,
		filesapi.CodeHashMismatch:        http.StatusBadRequest,
		filesapi.CodeUnavailable:         http.StatusServiceUnavailable,
	} {
		err := filesapi.ErrorFromCode(code)
		if err == nil {
			t.Fatalf("ErrorFromCode(%q) = nil", code)
		}
		gotCode, gotStatus := filesapi.CodeOf(fmt.Errorf("wrapped: %w", err))
		if gotCode != code || gotStatus != status {
			t.Errorf("CodeOf(%s) = %q, %d; want %q, %d", code, gotCode, gotStatus, code, status)
		}
	}
	if filesapi.ErrorFromCode("nope") != nil {
		t.Fatal("unknown code must map to nil")
	}
	if code, status := filesapi.CodeOf(errors.New("boom")); code != "" || status != http.StatusInternalServerError {
		t.Fatalf("CodeOf(other) = %q, %d", code, status)
	}
}

func TestStatusErrorIs(t *testing.T) {
	cases := []struct {
		err  *filesapi.StatusError
		want error
	}{
		{&filesapi.StatusError{Status: 409, Code: "exists"}, filesapi.ErrExists},
		{&filesapi.StatusError{Status: 409, Code: "modified"}, filesapi.ErrModified},
		{&filesapi.StatusError{Status: 507, Code: "insufficient_storage"}, filesapi.ErrInsufficientStorage},
		{&filesapi.StatusError{Status: 500}, filesapi.ErrUnavailable},
		{&filesapi.StatusError{Status: 418, Code: "teapot"}, filesapi.ErrUnavailable},
		{&filesapi.StatusError{Status: 400, Code: "hash_mismatch"}, filesapi.ErrHashMismatch},
		{&filesapi.StatusError{Status: 503, Code: "unavailable"}, filesapi.ErrBackupsUnavailable},
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%+v is not %v", c.err, c.want)
		}
	}
	if errors.Is(&filesapi.StatusError{Status: 507, Code: "insufficient_storage"}, filesapi.ErrUnavailable) {
		t.Fatal("a known code must not also match ErrUnavailable")
	}
	// hash_mismatch and invalid share the status 400 but are different sentinels.
	if errors.Is(&filesapi.StatusError{Status: 400, Code: "hash_mismatch"}, filesapi.ErrInvalid) {
		t.Fatal("hash_mismatch must not match ErrInvalid")
	}
	if errors.Is(&filesapi.StatusError{Status: 400, Code: "invalid"}, filesapi.ErrHashMismatch) {
		t.Fatal("invalid must not match ErrHashMismatch")
	}
	// "restic is missing" is not "the service is unreachable": the panel answers
	// backups_unavailable for the first and files_unavailable for the second.
	if errors.Is(&filesapi.StatusError{Status: 503, Code: "unavailable"}, filesapi.ErrUnavailable) {
		t.Fatal("code unavailable must not match ErrUnavailable")
	}
	if errors.Is(&filesapi.StatusError{Status: 503}, filesapi.ErrBackupsUnavailable) {
		t.Fatal("a 503 without code must not match ErrBackupsUnavailable")
	}
}

// TestStatusErrorNeedsMatchingStatus: a code only counts with its own HTTP status; anything
// else is a broken answer of the service.
func TestStatusErrorNeedsMatchingStatus(t *testing.T) {
	for _, e := range []*filesapi.StatusError{
		{Status: 500, Code: "not_found"},
		{Status: 200, Code: "exists"},
		{Status: 404, Code: "exists"},
	} {
		if !errors.Is(e, filesapi.ErrUnavailable) {
			t.Errorf("%+v is not ErrUnavailable", e)
		}
		if errors.Is(e, filesapi.ErrNotFound) || errors.Is(e, filesapi.ErrExists) {
			t.Errorf("%+v matches a sentinel despite its status", e)
		}
	}
}
