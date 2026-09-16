package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

func assertHandlerMutationsUseExecutor(t *testing.T, receiver string, methods []string) {
	t.Helper()
	declarations := handlerMethodDeclarations(t, receiver)
	for _, method := range methods {
		found := false
		if fn := declarations[method]; fn != nil {
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "executeMutation" {
					found = true
				}
				return true
			})
		}
		if !found {
			t.Errorf("%s.%s does not use executeMutation", receiver, method)
		}
	}
}

// handlerMethodDeclarations scans production files by receiver identity so
// splitting a handler cannot silently remove methods from contract coverage.
func handlerMethodDeclarations(t *testing.T, receiver string) map[string]*ast.FuncDecl {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	methods := map[string]*ast.FuncDecl{}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, declaration := range file.Decls {
			fn, ok := declaration.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Recv == nil {
				continue
			}
			typeExpr := fn.Recv.List[0].Type
			if pointer, ok := typeExpr.(*ast.StarExpr); ok {
				typeExpr = pointer.X
			}
			name, ok := typeExpr.(*ast.Ident)
			if !ok || name.Name != receiver {
				continue
			}
			methods[fn.Name.Name] = fn
		}
	}
	return methods
}

func parsedMethodCalls(t *testing.T, receiverType string) map[string]map[string]bool {
	t.Helper()
	calls := map[string]map[string]bool{}
	for name, fn := range handlerMethodDeclarations(t, receiverType) {
		calls[name] = map[string]bool{}
		if len(fn.Recv.List[0].Names) == 0 {
			continue
		}
		receiverName := fn.Recv.List[0].Names[0].Name
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if receiver, ok := selector.X.(*ast.Ident); ok && receiver.Name == receiverName {
				calls[name][selector.Sel.Name] = true
			}
			return true
		})
	}
	return calls
}
