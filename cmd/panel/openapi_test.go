package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Bernotese/mc-panel/internal/files/filesapi"
	"github.com/Bernotese/mc-panel/internal/httpx"
	"github.com/Bernotese/mc-panel/internal/plugins"
	"github.com/Bernotese/mc-panel/internal/plugins/upstream"
	"github.com/Bernotese/mc-panel/internal/serverfiles"
)

// openAPIOperations extracts "METHOD /api/path" for every operation in api/openapi.yaml.
// Stdlib only: the file's layout (two-space path keys, four-space method keys under paths:) is
// simple and stable enough for a line-based scan.
func openAPIOperations(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	pathRe := regexp.MustCompile(`^  (/[^:\s]*):\s*$`)
	methodRe := regexp.MustCompile(`^    (get|post|put|patch|delete):\s*$`)
	var ops []string
	inPaths, cur := false, ""
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case line == "paths:":
			inPaths = true
			continue
		case inPaths && line != "" && !strings.HasPrefix(line, " "):
			inPaths = false
		}
		if !inPaths {
			continue
		}
		if m := pathRe.FindStringSubmatch(line); m != nil {
			cur = m[1]
		} else if m := methodRe.FindStringSubmatch(line); m != nil && cur != "" {
			ops = append(ops, strings.ToUpper(m[1])+" /api"+cur)
		}
	}
	slices.Sort(ops)
	return ops
}

// The registered routes (method + pattern) are exactly the operations of the OpenAPI contract.
func TestRoutesMatchOpenAPI(t *testing.T) {
	a, _ := newTestApp(t)
	var routes []string
	for _, r := range a.Routes {
		routes = append(routes, r.Method+" "+r.Pattern)
	}
	slices.Sort(routes)
	spec := openAPIOperations(t)
	if len(spec) < 50 {
		t.Fatalf("parsed only %d operations from openapi.yaml: %v", len(spec), spec)
	}
	for _, op := range spec {
		if !slices.Contains(routes, op) {
			t.Errorf("OpenAPI operation not registered: %s", op)
		}
	}
	for _, r := range routes {
		if !slices.Contains(spec, r) {
			t.Errorf("route not in OpenAPI: %s", r)
		}
	}
}

// httpx's error codes (the Code* constants) are exactly the Error.error enum of the contract.
func TestErrorCodesMatchOpenAPI(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../../internal/httpx/errors.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range vs.Names {
			if strings.HasPrefix(name.Name, "Code") && i < len(vs.Values) {
				if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					codes = append(codes, v)
				}
			}
		}
		return true
	})
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^    Error:\n(?:      .*\n)*?        error: \{ type: string, enum: \[([^\]]*)\] \}`).FindSubmatch(raw)
	if m == nil {
		t.Fatal("Error.error enum not found in openapi.yaml")
	}
	var enum []string
	for _, v := range strings.Split(string(m[1]), ",") {
		enum = append(enum, strings.TrimSpace(v))
	}
	slices.Sort(codes)
	slices.Sort(enum)
	if len(codes) < 10 || !slices.Equal(codes, enum) {
		t.Fatalf("httpx codes %v\n!= OpenAPI enum %v", codes, enum)
	}
}

// Review K7: streamConsole answers an invalid ?tail with 400 validation_failed; the contract
// must declare it.
func TestStreamConsoleDeclaresBadRequest(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	start := strings.Index(spec, "operationId: streamConsole")
	if start < 0 {
		t.Fatal("streamConsole not found in openapi.yaml")
	}
	op := spec[start:]
	if end := strings.Index(op, "\n  /"); end >= 0 { // the next path
		op = op[:end]
	}
	if !strings.Contains(op, "'400': { $ref: '#/components/responses/BadRequest' }") {
		t.Fatalf("streamConsole declares no 400 response:\n%s", op)
	}
}

// openAPIOperation returns the lines of one operation of api/openapi.yaml (from its
// operationId to the next method or path).
func openAPIOperation(t *testing.T, spec, operationID string) string {
	t.Helper()
	start := strings.Index(spec, "operationId: "+operationID+"\n")
	if start < 0 {
		t.Fatalf("operation %s not found in openapi.yaml", operationID)
	}
	op := spec[start:]
	if loc := regexp.MustCompile(`\n  /|\n    (get|post|put|patch|delete):`).FindStringIndex(op); loc != nil {
		op = op[:loc[0]]
	}
	return op
}

// responseText returns the description of one response of an operation, resolving a $ref to
// components/responses; ok is false if the operation does not declare the status.
func responseText(spec, op string, status int) (string, bool) {
	m := regexp.MustCompile(`(?m)^        '` + strconv.Itoa(status) + `': (.*)$`).FindStringSubmatch(op)
	if m == nil {
		return "", false
	}
	if ref := regexp.MustCompile(`\$ref: '#/components/responses/(\w+)'`).FindStringSubmatch(m[1]); ref != nil {
		c := regexp.MustCompile(`(?m)^    ` + ref[1] + `: (.*)$`).FindStringSubmatch(spec[strings.Index(spec, "\n  responses:\n"):])
		if c == nil {
			return "", false
		}
		return ref[1] + ": " + c[1], true
	}
	return m[1], true
}

// Review B2: every status and error code a files route can answer - the service errors through
// serverfiles' error mapping plus the panel's own checks - is declared by the operation in
// api/openapi.yaml, and its description names the code (shared responses name their default
// code by their purpose: BadRequest validation_failed, NotFound not_found, TooManyRequests
// rate_limited).
func TestFilesErrorsMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	type answer struct {
		status int
		code   string
	}
	// Answers of the panel itself: path/body validation and the server lookup on every route.
	common := []answer{{400, httpx.CodeValidation}, {404, httpx.CodeNotFound}}
	limited := answer{429, httpx.CodeRateLimited} // per-user or global limits of the panel
	ops := []struct {
		id    string
		errs  []error // what the service call can fail with
		extra []answer
	}{
		{"listFiles", []error{filesapi.ErrNotFound, filesapi.ErrNotRegular, filesapi.ErrInvalid, filesapi.ErrUnavailable}, nil},
		{"readFile", []error{filesapi.ErrNotFound, filesapi.ErrNotRegular, filesapi.ErrNotText, filesapi.ErrInvalid, filesapi.ErrUnavailable}, []answer{limited}},
		{"writeFile", []error{
			filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrModified, filesapi.ErrNotRegular, filesapi.ErrTooLarge,
			filesapi.ErrInsufficientStorage, filesapi.ErrInvalid, filesapi.ErrUnavailable,
		}, []answer{limited}},
		{"uploadFile", []error{
			filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrNotRegular, filesapi.ErrTooLarge, filesapi.ErrInsufficientStorage,
			filesapi.ErrBusy, filesapi.ErrInvalid, filesapi.ErrUnavailable,
		}, []answer{limited}},
		{"downloadFile", []error{
			filesapi.ErrNotFound, filesapi.ErrNotRegular, filesapi.ErrTooLarge, filesapi.ErrBusy, filesapi.ErrInvalid, filesapi.ErrUnavailable,
		}, []answer{limited}},
		{"makeDirectory", []error{
			filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrNotRegular, filesapi.ErrTooLarge, filesapi.ErrInsufficientStorage,
			filesapi.ErrInvalid, filesapi.ErrUnavailable,
		}, nil},
		{"renameFile", []error{
			filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrNotRegular, filesapi.ErrTooLarge, filesapi.ErrInsufficientStorage,
			filesapi.ErrInvalid, filesapi.ErrUnavailable,
		}, nil},
		{"deleteFile", []error{filesapi.ErrNotFound, filesapi.ErrNotRegular, filesapi.ErrTooLarge, filesapi.ErrInvalid, filesapi.ErrUnavailable}, nil},
		{"extractArchive", []error{
			filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrNotRegular, filesapi.ErrTooLarge, filesapi.ErrInsufficientStorage,
			filesapi.ErrBusy, filesapi.ErrInvalid, filesapi.ErrUnavailable,
		}, []answer{{409, httpx.CodeInstanceRunning}, {503, httpx.CodeAgentUnavailable}, {503, httpx.CodeDockerUnavailable}}},
		{"getFileJob", []error{filesapi.ErrUnavailable}, []answer{{404, httpx.CodeNotFound}}},
	}
	defaults := map[string]string{"BadRequest": httpx.CodeValidation, "NotFound": httpx.CodeNotFound, "TooManyRequests": httpx.CodeRateLimited}
	for _, o := range ops {
		op := openAPIOperation(t, spec, o.id)
		want := slices.Clone(o.extra)
		if o.id != "getFileJob" {
			want = append(want, common...)
		}
		for _, e := range o.errs {
			status, code := serverfiles.ErrorStatus(fmt.Errorf("service: %w", e))
			want = append(want, answer{status, code})
		}
		for _, a := range want {
			text, ok := responseText(spec, op, a.status)
			switch {
			case !ok:
				t.Errorf("%s: answers %d %s, but declares no %d response", o.id, a.status, a.code, a.status)
			case strings.Contains(text, a.code):
			case defaults[strings.SplitN(text, ":", 2)[0]] == a.code:
			default:
				t.Errorf("%s: the %d response does not name %s: %s", o.id, a.status, a.code, text)
			}
		}
	}
}

// Every status and error code a plugin route can answer - the mapped service and upstream
// errors plus the panel's own checks - is declared by its operation in api/openapi.yaml, and
// the description names the code (shared responses: BadRequest = validation_failed, NotFound
// = not_found, TooManyRequests = rate_limited).
func TestPluginsErrorsMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(raw)
	type answer struct {
		status int
		code   string
	}
	unsupported := []answer{{404, httpx.CodeNotFound}, {409, httpx.CodePluginsUnsupported}}
	lookup := []answer{
		{400, httpx.CodeValidation}, {429, httpx.CodeRateLimited},
		{502, httpx.CodePluginSourceUnavailable}, {502, httpx.CodeCatalogUnavailable},
	}
	ops := []struct {
		id    string
		errs  []error
		extra []answer
	}{
		{"listPlugins", []error{filesapi.ErrUnavailable}, unsupported},
		{"searchPlugins", []error{upstream.ErrUnavailable}, append(slices.Clone(unsupported), lookup...)},
		{"listPluginVersions", []error{upstream.ErrUnavailable, upstream.ErrNotFound}, append(slices.Clone(unsupported), lookup...)},
		{"resolvePlugin", []error{upstream.ErrUnavailable, upstream.ErrNotFound, filesapi.ErrUnavailable},
			append(append(slices.Clone(unsupported), lookup...), answer{409, httpx.CodePluginIncompatible}, answer{409, httpx.CodePluginNotDownloadable})},
		{"installPlugin", []error{upstream.ErrUnavailable, upstream.ErrNotFound, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict},
				answer{409, httpx.CodePluginIncompatible}, answer{409, httpx.CodePluginNotDownloadable}, answer{409, httpx.CodeFileExists},
				answer{429, httpx.CodeRateLimited}, answer{502, httpx.CodePluginSourceUnavailable}, answer{502, httpx.CodeCatalogUnavailable})},
		{"getPluginUpdates", []error{upstream.ErrUnavailable, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{429, httpx.CodeRateLimited}, answer{502, httpx.CodeCatalogUnavailable})},
		{"updatePlugins", []error{upstream.ErrUnavailable, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict},
				answer{429, httpx.CodeRateLimited}, answer{502, httpx.CodeCatalogUnavailable})},
		{"togglePlugin", []error{filesapi.ErrNotFound, filesapi.ErrExists, filesapi.ErrInvalid, filesapi.ErrNotRegular, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict})},
		{"deletePlugin", []error{filesapi.ErrNotFound, filesapi.ErrInvalid, filesapi.ErrNotRegular, filesapi.ErrUnavailable},
			append(slices.Clone(unsupported), answer{400, httpx.CodeValidation}, answer{409, httpx.CodeConflict})},
		{"getPluginJob", nil, unsupported},
		{"getPluginIcon", nil, []answer{{400, httpx.CodeValidation}, {404, httpx.CodeNotFound}, {502, httpx.CodePluginSourceUnavailable}}},
		{"previewMrpack", []error{upstream.ErrUnavailable, filesapi.ErrUnavailable, plugins.ErrMrpackIncompatible},
			append(append(slices.Clone(unsupported), lookup...), answer{413, httpx.CodePayloadTooLarge})},
		{"importMrpack", []error{upstream.ErrUnavailable, filesapi.ErrUnavailable, plugins.ErrMrpackIncompatible, plugins.ErrMrpackMismatch, plugins.ErrNothingToImport},
			append(append(slices.Clone(unsupported), lookup...), answer{409, httpx.CodeConflict}, answer{413, httpx.CodePayloadTooLarge})},
	}
	defaults := map[string]string{"BadRequest": httpx.CodeValidation, "NotFound": httpx.CodeNotFound, "TooManyRequests": httpx.CodeRateLimited}
	for _, o := range ops {
		op := openAPIOperation(t, spec, o.id)
		want := slices.Clone(o.extra)
		for _, e := range o.errs {
			status, code := plugins.ErrorStatus(fmt.Errorf("wrapped: %w", e))
			want = append(want, answer{status, code})
		}
		for _, a := range want {
			text, ok := responseText(spec, op, a.status)
			switch {
			case !ok:
				t.Errorf("%s: answers %d %s, but declares no %d response", o.id, a.status, a.code, a.status)
			case strings.Contains(text, a.code):
			case defaults[strings.SplitN(text, ":", 2)[0]] == a.code:
			default:
				t.Errorf("%s: the %d response does not name %s: %s", o.id, a.status, a.code, text)
			}
		}
	}
}
