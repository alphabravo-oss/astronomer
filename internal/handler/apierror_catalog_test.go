package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestApierrorCatalogCoverage enforces that every RespondRequestError call site
// passes its `code` (4th positional) argument as an apierror.* catalog selector
// rather than a bare string literal.
//
// The Track B2 codemod expanded internal/handler/apierror/codes.go to the full
// canonical catalog and rewrote all ~2,000 legacy bare-literal call sites to
// reference constants. This test guards that migration: a newly introduced
// RespondRequestError(..., "some_code", ...) literal will fail here, pointing
// the author at the catalog.
//
// A handful of shared error-translation helpers pass a code that is computed at
// runtime (a local variable or a struct field) rather than a string literal:
//
//   - response.go: the RespondRequestError definition itself (param `code`).
//   - authorization.go: requireSuperuser forwards cfg.InvalidUserCode /
//     cfg.StoreUnavailableCode (each falling back to a catalog string).
//   - agent_fleet.go: respondAgentFleetError forwards handlerErr.code from a
//     typed *agentFleetHandlerError.
//
// Those arguments are not string literals, so they are inherently outside this
// lint (it only flags *bare literal* code args) and need no allowlist. If one
// of them is ever changed to pass a literal directly, this test will flag it.
func TestApierrorCatalogCoverage(t *testing.T) {
	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob handler files: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string

	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "RespondRequestError" {
				return true
			}
			if len(call.Args) < 4 {
				return true
			}
			lit, ok := call.Args[3].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true // dynamic (variable / selector) code arg — out of scope
			}
			val, _ := strconv.Unquote(lit.Value)
			pos := fset.Position(lit.Pos())
			offenders = append(offenders,
				pos.Filename+":"+strconv.Itoa(pos.Line)+": bare code literal "+
					strconv.Quote(val)+
					" (use an apierror.* constant from internal/handler/apierror/codes.go)")
			return true
		})
	}

	if len(offenders) > 0 {
		t.Errorf("RespondRequestError calls passing a bare error-code string literal instead of an apierror.* constant:\n%s",
			strings.Join(offenders, "\n"))
	}
}
