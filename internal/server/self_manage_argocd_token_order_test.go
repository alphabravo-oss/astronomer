package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestArgoTokenRefreshIsNotGatedByLegacyPreflight pins the ordering that makes
// ArgoCD adoption self-healing.
//
// reconcileLocalArgoSelfManagement must refresh the ArgoCD session token
// (loginToArgoCDWithInitialAdminSecret -> ensureLocalArgoInstanceRow) BEFORE it
// calls preflightSelfManagedApplicationCredentialMigration.
//
// The preflight fails closed and demands a quiesced Argo controller. When the
// token refresh sat *after* it, any preflight failure meant the token was never
// minted or renewed — and ensureLocalArgoInstanceRow deliberately preserves the
// stored token when the mint yields "" (so a transient argocd-server blip can't
// sever adoption). Together that let argocd_instances keep an EXPIRED token
// forever: cluster auto-registration failed 100% of attempts with
// 401 "token is expired" and only recovered when an operator manually blanked
// auth_token_encrypted in Postgres.
//
// Minting a session is an HTTP login plus a write to our own argocd_instances
// row — not a cluster Secret or Application mutation — so it sits outside what
// that gate exists to protect. Keep it ahead of the gate.
//
// There is no live-Postgres/Argo harness in this package (the reconcile takes a
// concrete *sqlc.Queries), so this pins the invariant statically — the same
// approach the ArgoCD operation-claim tests use for their SQL.
func TestArgoTokenRefreshIsNotGatedByLegacyPreflight(t *testing.T) {
	const (
		fn        = "reconcileLocalArgoSelfManagement"
		mint      = "loginToArgoCDWithInitialAdminSecret"
		preflight = "preflightSelfManagedApplicationCredentialMigration"
	)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "self_manage_argocd.go", nil, 0)
	if err != nil {
		t.Fatalf("parse self_manage_argocd.go: %v", err)
	}

	var body *ast.BlockStmt
	for _, decl := range file.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name.Name == fn {
			body = fd.Body
			break
		}
	}
	if body == nil {
		t.Fatalf("function %s not found — if it was renamed, re-point this guard rather than deleting it", fn)
	}

	pos := map[string]int{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if _, seen := pos[ident.Name]; seen {
			return true
		}
		if ident.Name == mint || ident.Name == preflight {
			pos[ident.Name] = int(call.Pos())
		}
		return true
	})

	mintPos, okMint := pos[mint]
	preflightPos, okPre := pos[preflight]
	if !okMint {
		t.Fatalf("%s no longer calls %s — the ArgoCD session token would never be refreshed", fn, mint)
	}
	if !okPre {
		t.Skipf("%s no longer calls %s; the ordering guard is moot", fn, preflight)
	}

	if mintPos > preflightPos {
		t.Errorf("%s calls %s AFTER %s.\n"+
			"The preflight fails closed (it demands a quiesced Argo controller), so gating the token\n"+
			"refresh behind it lets argocd_instances keep an expired token indefinitely — cluster\n"+
			"auto-registration then fails 100%% with 401 \"token is expired\" until someone hand-edits\n"+
			"the database. Move the token refresh back above the preflight.", fn, mint, preflight)
	}
}
