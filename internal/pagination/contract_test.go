package pagination_test

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

// Every handler package uses the shared page model; adding a new domain must
// not quietly reintroduce a second page contract or pagination URL builder.
func TestHandlersDoNotReintroduceParallelPageContracts(t *testing.T) {
	retired := map[string]bool{
		"RespondPaginated": true, "RespondList": true, "respondPage": true,
		"paginatedResponse": true, "pageEnvelope": true, "Pagination": true,
		"NewPagination": true, "NewPaginationFromPage": true, "parseLimitOffset": true,
	}
	err := filepath.WalkDir("../handler", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.FuncDecl:
				if retired[value.Name.Name] {
					t.Errorf("%s declares retired page helper %s", path, value.Name.Name)
				}
			case *ast.TypeSpec:
				if retired[value.Name.Name] {
					t.Errorf("%s declares retired page model %s", path, value.Name.Name)
				}
			case *ast.CompositeLit:
				if _, ok := value.Type.(*ast.MapType); !ok {
					break
				}
				keys := map[string]bool{}
				for _, element := range value.Elts {
					pair, ok := element.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					literal, ok := pair.Key.(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					key, err := strconv.Unquote(literal.Value)
					if err == nil {
						keys[key] = true
					}
				}
				if keys["items"] && (keys["limit"] || keys["offset"]) {
					t.Errorf("%s constructs a parallel items/limit/offset envelope; use pagination.Response", path)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// This bounded syntax check requires the explicit request guard at direct
// offset/slice producer boundaries. It is not a whole-callgraph proof.
func TestLegacyOffsetHTTPProducersValidateBeforeWork(t *testing.T) {
	err := filepath.WalkDir("../handler", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			writer := false
			for _, param := range fn.Type.Params.List {
				if typ, ok := param.Type.(*ast.SelectorExpr); ok && typ.Sel.Name == "ResponseWriter" {
					writer = true
				}
			}
			if !writer {
				continue
			}
			produces := false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				name, ok := call.Fun.(*ast.Ident)
				if ok && (name.Name == "queryOffset" || name.Name == "queryLimitOffset" || name.Name == "pageWindow") {
					produces = true
				}
				return true
			})
			if !produces {
				continue
			}
			guarded := false
			if len(fn.Body.List) > 0 {
				if first, ok := fn.Body.List[0].(*ast.IfStmt); ok && first.Init == nil && first.Else == nil {
					if negation, ok := first.Cond.(*ast.UnaryExpr); ok && negation.Op == token.NOT {
						if call, ok := negation.X.(*ast.CallExpr); ok {
							if name, ok := call.Fun.(*ast.Ident); ok && (name.Name == "validateLegacyPage" || name.Name == "validatePageOffset") {
								if len(first.Body.List) == 1 {
									_, returns := first.Body.List[0].(*ast.ReturnStmt)
									guarded = returns
								}
							}
						}
					}
				}
			}
			if !guarded {
				t.Errorf("%s:%s may clamp/slice an unsupported continuation before validating", path, fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
