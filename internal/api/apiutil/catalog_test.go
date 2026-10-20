package apiutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestCatalogCoversEveryCode proves the catalog is closed in both directions:
// every Code constant declared in codes.go has a transport row, and every row
// names a declared constant. Wire values are unique.
func TestCatalogCoversEveryCode(t *testing.T) {
	t.Parallel()
	declared := declaredCodes(t)
	seen := map[string]string{}
	for name, value := range declared {
		if other, dup := seen[value]; dup {
			t.Errorf("wire value %q declared twice (%s, %s)", value, other, name)
		}
		seen[value] = name
		if !Known(Code(value)) {
			t.Errorf("code %s (%q) has no catalog row", name, value)
		}
	}
	for code := range catalog {
		if _, ok := seen[string(code)]; !ok {
			t.Errorf("catalog row %q names no constant in codes.go", code)
		}
	}
	if len(catalog) != len(declared) {
		t.Errorf("catalog has %d rows, codes.go declares %d codes", len(catalog), len(declared))
	}
}

// TestCatalogStatusesAgreeWithConnectCodes checks each row's HTTP status and
// connect.Code describe the same failure family.
func TestCatalogStatusesAgreeWithConnectCodes(t *testing.T) {
	t.Parallel()
	narrowed := map[Code]bool{
		AlreadyExists: true, Conflict: true, ConcurrentModification: true, IdempotencyConflict: true,
		StaleGeneration: true, StaleResourceVersion: true, PreviewCursorStale: true, RolloutPreviewStale: true,
		RolloutTargetChanged: true, RolloutLeaseLost: true, RolloutStaleFence: true, RolloutInvariant: true,
		AgentOverloaded: true, RequestCanceled: true,
	}
	for code, row := range catalog {
		if narrowed[code] {
			continue
		}
		if want := ConnectCodeForHTTPStatus(row.status); want != row.connect {
			t.Errorf("%s: status %d implies %s, row says %s", code, row.status, want, row.connect)
		}
	}
}

func declaredCodes(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "codes.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if ident, ok := vs.Type.(*ast.Ident); !ok || ident.Name != "Code" {
				t.Fatalf("const %v in codes.go is not of type Code", vs.Names)
			}
			for i, name := range vs.Names {
				lit := vs.Values[i].(*ast.BasicLit)
				value, _ := strconv.Unquote(lit.Value)
				out[name.Name] = value
			}
		}
	}
	return out
}

// TestNoCodeOutsideCatalog scans every non-test Go file under internal/ and
// cmd/ for the two ways to smuggle a code past the defined type: an untyped
// string literal passed as WriteError's code argument, and an explicit
// apiutil.Code(...) conversion.
func TestNoCodeOutsideCatalog(t *testing.T) {
	t.Parallel()
	var offenders []string
	for _, root := range []string{"../..", "../../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			offenders = append(offenders, codeOffenders(fset, file)...)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("error codes must be apiutil constants from codes.go:\n%s", strings.Join(offenders, "\n"))
	}
}

func codeOffenders(fset *token.FileSet, file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := calleeName(call.Fun)
		if name == "apiutil.Code" {
			out = append(out, fset.Position(call.Pos()).String()+": apiutil.Code(...) conversion")
		}
		if (name == "apiutil.WriteError" || name == "WriteError") && len(call.Args) == 5 {
			if lit, ok := call.Args[3].(*ast.BasicLit); ok {
				out = append(out, fset.Position(lit.Pos()).String()+": bare code literal "+lit.Value)
			}
		}
		return true
	})
	return out
}

func calleeName(fun ast.Expr) string {
	switch fn := fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if pkg, ok := fn.X.(*ast.Ident); ok {
			return pkg.Name + "." + fn.Sel.Name
		}
	}
	return ""
}
