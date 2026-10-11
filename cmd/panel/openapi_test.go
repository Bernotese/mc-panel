package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
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
