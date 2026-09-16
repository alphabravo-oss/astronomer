package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInteractiveSessionMintPathsUseContextProvider guards the complete
// interactive mint surface. The runtime TTL lives on JWTManager, so each
// handler must delegate through context-aware token generation or preparation; a future direct
// GenerateAccessToken/GenerateTokenPair call would silently bypass request
// cancellation or make one login mode diverge from session.timeout_minutes.
func TestInteractiveSessionMintPathsUseContextProvider(t *testing.T) {
	tests := []struct {
		file     string
		receiver string
		function string
	}{
		{file: "auth.go", receiver: "AuthHandler", function: "Login"},
		{file: "auth.go", receiver: "AuthHandler", function: "Refresh"},
		{file: "sso_tx.go", receiver: "SSOHandler", function: "commitSSOCallback"},
		{file: "totp.go", receiver: "TOTPHandler", function: "EnrollConfirm"},
		{file: "totp_verify.go", receiver: "TOTPHandler", function: "Verify"},
	}

	for _, tt := range tests {
		t.Run(tt.receiver+"."+tt.function, func(t *testing.T) {
			fn := findSessionMintMethod(t, tt.file, tt.receiver, tt.function)
			if fn == nil {
				t.Fatalf("method %s.%s not found in %s", tt.receiver, tt.function, tt.file)
				return
			}

			contextCalls := 0
			legacyCalls := 0
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch sel.Sel.Name {
				case "GenerateTokenPairContext", "PrepareTokenPairContext":
					contextCalls++
				case "GenerateTokenPair", "GenerateAccessToken", "GenerateAccessTokenContext":
					legacyCalls++
				}
				return true
			})
			if contextCalls == 0 {
				t.Fatalf("%s.%s does not use context-aware token generation or preparation", tt.receiver, tt.function)
			}
			if legacyCalls != 0 {
				t.Fatalf("%s.%s has %d direct/legacy access-token mint call(s)", tt.receiver, tt.function, legacyCalls)
			}
		})
	}
}

func findSessionMintMethod(t *testing.T, fileName, receiver, name string) *ast.FuncDecl {
	if receiver != "AuthHandler" {
		file, err := parser.ParseFile(token.NewFileSet(), fileName, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", fileName, err)
		}
		return findMethod(file, receiver, name)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		candidate := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(candidate, "auth") || !strings.HasSuffix(candidate, ".go") || strings.HasSuffix(candidate, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", candidate), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		if method := findMethod(file, receiver, name); method != nil {
			return method
		}
	}
	return nil
}

func findMethod(file *ast.File, receiver, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		receiverType := fn.Recv.List[0].Type
		if ptr, ok := receiverType.(*ast.StarExpr); ok {
			receiverType = ptr.X
		}
		if ident, ok := receiverType.(*ast.Ident); ok && ident.Name == receiver {
			return fn
		}
	}
	return nil
}
