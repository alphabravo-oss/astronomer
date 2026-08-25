package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// Guard every public mutation entry point. Behavioral tests prove rollback
// and ordering; this source-level check prevents a future refactor from
// silently bypassing the typed production seam in just one route.
func TestEveryResourceSettingsSSOMutationHasTransactionalPath(t *testing.T) {
	path, err := filepath.Abs("resources_settings_sso.go")
	if err != nil {
		t.Fatal(err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	type expectation struct {
		runTxSelector string
		txHelper      string
		found         bool
	}
	want := map[string]*expectation{
		"UpdateGeneralSettings": {runTxSelector: "runTx"},
		"CreateSSOProvider":     {txHelper: "createSSOProviderTx"},
		"DeleteSSOProvider":     {txHelper: "deleteSSOProviderTx"},
	}
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		expected, tracked := want[fn.Name.Name]
		if !tracked {
			continue
		}
		ast.Inspect(fn.Body, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.SelectorExpr:
				if expected.runTxSelector != "" && value.Sel.Name == expected.runTxSelector {
					expected.found = true
				}
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if ok && expected.txHelper != "" && selector.Sel.Name == expected.txHelper {
					expected.found = true
				}
			}
			return true
		})
	}
	for name, expected := range want {
		if !expected.found {
			t.Errorf("%s no longer exposes its typed transactional path", name)
		}
	}
}
