package audit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

var auditActionPattern = regexp.MustCompile(`^[a-z]+(\.[a-z0-9_]+)+$`)

func TestLiteralAuditActionsMatchContract(t *testing.T) {
	files, err := filepath.Glob("../handler/*.go")
	if err != nil {
		t.Fatalf("glob handler files: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0

	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}

			actionArgIndex := -1
			switch ident.Name {
			case "recordAudit":
				actionArgIndex = 2
			case "recordAuditAs":
				actionArgIndex = 3
			default:
				return true
			}

			if len(call.Args) <= actionArgIndex {
				t.Fatalf("%s: malformed %s call", path, ident.Name)
			}
			lit, ok := call.Args[actionArgIndex].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			action, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: unquote action %s: %v", path, lit.Value, err)
			}
			checked++
			if !auditActionPattern.MatchString(action) {
				t.Errorf("%s: audit action %q does not match %s", path, action, auditActionPattern.String())
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatal("expected to validate at least one literal audit action")
	}
}
