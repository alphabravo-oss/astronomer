package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// Direct process-environment access is restricted to configuration and true
// executable/bootstrap adapters. Application and domain packages receive
// typed values through Config instead of hiding process-global dependencies.
var directEnvAccessAllowlist = map[string]string{
	"internal/agent/config.go":            "agent process configuration loader",
	"internal/agent/helm.go":              "Helm SDK standard environment adapter",
	"internal/agent/token_persistence.go": "in-cluster process detection before token persistence",
	"internal/agent/upgrade_watchdog.go":  "standalone watchdog process bootstrap",
	"internal/astrocli/config.go":         "CLI configuration-directory bootstrap",
	"internal/charlie/activation_helm.go": "Helm SDK standard environment adapter",
}

func TestDirectEnvironmentAccessIsCentralized(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve contract test path")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	internalRoot := filepath.Join(root, "internal")
	fset := token.NewFileSet()
	seenAllowlisted := map[string]bool{}
	var violations []string

	err := filepath.WalkDir(internalRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		osAliases := map[string]bool{}
		osDotImport := false
		for _, imported := range file.Imports {
			if imported.Path.Value != `"os"` {
				continue
			}
			if imported.Name == nil {
				osAliases["os"] = true
			} else if imported.Name.Name == "." {
				osDotImport = true
			} else {
				osAliases[imported.Name.Name] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			isDirectEnvCall := false
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				pkg, packageOK := selector.X.(*ast.Ident)
				isDirectEnvCall = packageOK && osAliases[pkg.Name] && (selector.Sel.Name == "Getenv" || selector.Sel.Name == "LookupEnv")
			} else if function, ok := call.Fun.(*ast.Ident); ok && osDotImport {
				isDirectEnvCall = function.Name == "Getenv" || function.Name == "LookupEnv"
			}
			if !isDirectEnvCall {
				return true
			}
			if strings.HasPrefix(rel, "internal/config/") {
				return true
			}
			if _, allowed := directEnvAccessAllowlist[rel]; allowed {
				seenAllowlisted[rel] = true
				return true
			}
			position := fset.Position(call.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d", rel, position.Line))
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan direct environment access: %v", err)
	}

	for path, reason := range directEnvAccessAllowlist {
		if !seenAllowlisted[path] {
			violations = append(violations, fmt.Sprintf("stale allowlist entry %s (%s)", path, reason))
		}
	}
	sort.Strings(violations)
	if len(violations) > 0 {
		t.Fatalf("direct environment access must be resolved in internal/config and injected as typed values:\n%s", strings.Join(violations, "\n"))
	}
}
