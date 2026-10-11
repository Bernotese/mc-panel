package serverfiles

import (
	"net/http"
	"regexp"
)

var (
	uploadPath  = regexp.MustCompile(`^/api/servers/[0-9]+/files/upload$`)
	contentPath = regexp.MustCompile(`^/api/servers/[0-9]+/files/content$`)
)

// UnlimitedBody selects the requests exempt from the global 1 MiB body limit
// (httpx.BodyLimitExcept in cmd/panel, decision 1): the upload stream (read up to its
// Content-Length; the files service enforces its limit, spec 3b §5.1) and the editor's save
// (limited to filesapi.MaxJSONBytes in handleWrite). The match is exact and case-sensitive like
// chi; a path with escapes (RawPath set, e.g. "files%2Fupload") is routed by chi on its escaped
// form and never reaches these routes, so it keeps the limit.
func UnlimitedBody(r *http.Request) bool {
	if r.URL.RawPath != "" {
		return false
	}
	switch r.Method {
	case http.MethodPost:
		return uploadPath.MatchString(r.URL.Path)
	case http.MethodPut:
		return contentPath.MatchString(r.URL.Path)
	}
	return false
}
