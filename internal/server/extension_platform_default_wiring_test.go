package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// TestProductionExtensionAndPlatformDefaultTransactionsWired guards the exact
// NewApp production wiring. Handler-level SetRunTx tests are insufficient: a
// future constructor edit could leave the seam correct but never supply the
// database transaction in production.
func TestProductionExtensionAndPlatformDefaultTransactionsWired(t *testing.T) {
	path, err := filepath.Abs("app_router_dependencies_platform.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"ExtensionMutationTx":               false,
		"PlatformDefaultTemplateMutationTx": false,
	}
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		setRunTx, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || setRunTx.Sel.Name != "SetRunTx" {
			return true
		}
		runnerCall, ok := call.Args[0].(*ast.CallExpr)
		if !ok || len(runnerCall.Args) != 1 {
			return true
		}
		database, ok := runnerCall.Args[0].(*ast.Ident)
		if !ok || database.Name != "database" {
			return true
		}
		indexed, ok := runnerCall.Fun.(*ast.IndexExpr)
		if !ok {
			return true
		}
		runner, ok := indexed.X.(*ast.Ident)
		if !ok || runner.Name != "sqlcMutationTxRunner" {
			return true
		}
		txType, ok := indexed.Index.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := txType.X.(*ast.Ident)
		if !ok || pkg.Name != "handler" {
			return true
		}
		if _, tracked := want[txType.Sel.Name]; tracked {
			want[txType.Sel.Name] = true
		}
		return true
	})
	for txType, wired := range want {
		if !wired {
			t.Errorf("production constructor does not call SetRunTx(sqlcMutationTxRunner[handler.%s](database))", txType)
		}
	}
}
