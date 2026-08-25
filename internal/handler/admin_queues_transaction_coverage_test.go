package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

func TestEveryAdminQueueMutationEntersDurableOperationPath(t *testing.T) {
	path, err := filepath.Abs("admin_queues.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"RetryDLQ": false, "DiscardDLQ": false}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if _, tracked := want[fn.Name.Name]; !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if ok && selector.Sel.Name == "createOperation" {
				want[fn.Name.Name] = true
			}
			return true
		})
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s bypasses createOperation durable transaction path", name)
		}
	}
}

func TestAdminQueueDurablePathCommitsStateTaskAndAudit(t *testing.T) {
	path, err := filepath.Abs("admin_queues.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"CreateAdminQueueOperation": false,
		"EnqueueTaskOutbox":         false,
		"recordAuditOutbox":         false,
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "createOperation" || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch fun := call.Fun.(type) {
			case *ast.SelectorExpr:
				if _, tracked := want[fun.Sel.Name]; tracked {
					want[fun.Sel.Name] = true
				}
			case *ast.Ident:
				if _, tracked := want[fun.Name]; tracked {
					want[fun.Name] = true
				}
			}
			return true
		})
	}
	for call, found := range want {
		if !found {
			t.Errorf("durable path no longer calls %s", call)
		}
	}
}
