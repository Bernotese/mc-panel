package serverfiles

import (
	"net/http"
	"regexp"
)

var uploadPath = regexp.MustCompile(`^/api/servers/[0-9]+/files/upload$`)

// UnlimitedBody selects the requests exempt from the global 1 MiB body limit: only the upload
// stream (its own limit is enforced by the files service, spec 3b §5.1). The match is exact and
// case-sensitive like chi; a path with escapes (RawPath set, e.g. "files%2Fupload") is routed by
// chi on its escaped form and never reaches the upload route, so it keeps the limit.
func UnlimitedBody(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.RawPath == "" && uploadPath.MatchString(r.URL.Path)
}
