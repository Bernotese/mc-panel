package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
)

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("write json", "err", err)
	}
}

// DecodeJSON strictly decodes a single JSON value into dst. On failure it writes
// the error response (400 or 413) and returns false.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large")
			return false
		}
		WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "invalid JSON body")
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			WriteError(w, r, http.StatusRequestEntityTooLarge, CodePayloadTooLarge, "request body too large")
			return false
		}
		WriteError(w, r, http.StatusBadRequest, CodeBadRequest, "unexpected data after JSON body")
		return false
	}
	return true
}
