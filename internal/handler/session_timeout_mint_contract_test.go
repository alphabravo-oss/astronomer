package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestInteractiveSessionMintPathsUseContextProvider guards the complete
// interactive mint surface. The runtime TTL lives on JWTManager, so each
// handler must delegate through GenerateTokenPairContext; a future direct
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
		{file: "sso.go", receiver: "SSOHandler", function: "Callback"},
		{file: "totp.go", receiver: "TOTPHandler", function: "EnrollConfirm"},
		{file: "totp.go", receiver: "TOTPHandler", function: "Verify"},
	}

	for _, tt := range tests {
		t.Run(tt.receiver+"."+tt.function, func(t *testing.T) {
			file, err := parser.ParseFile(token.NewFileSet(), tt.file, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", tt.file, err)
			}
			fn := findMethod(file, tt.receiver, tt.function)
			if fn == nil {
				t.Fatalf("method %s.%s not found in %s", tt.receiver, tt.function, tt.file)
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
				case "GenerateTokenPairContext":
					contextCalls++
				case "GenerateTokenPair", "GenerateAccessToken", "GenerateAccessTokenContext":
					legacyCalls++
				}
				return true
			})
			if contextCalls == 0 {
				t.Fatalf("%s.%s does not use GenerateTokenPairContext", tt.receiver, tt.function)
			}
			if legacyCalls != 0 {
				t.Fatalf("%s.%s has %d direct/legacy access-token mint call(s)", tt.receiver, tt.function, legacyCalls)
			}
		})
	}
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
