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
	}
	for _, c := range cases {
		if !errors.Is(c.err, c.want) {
			t.Errorf("%+v is not %v", c.err, c.want)
		}
	}
	if errors.Is(&filesapi.StatusError{Status: 507, Code: "insufficient_storage"}, filesapi.ErrUnavailable) {
		t.Fatal("a known code must not also match ErrUnavailable")
	}
}
