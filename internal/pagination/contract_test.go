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
