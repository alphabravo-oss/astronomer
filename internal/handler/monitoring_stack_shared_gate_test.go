package handler

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The structural half of the shared-stack fence.
//
// TestSharedStackLifecycleDeniesCallerWithoutMonitoringPermission (in
// monitoring_stack_test.go) drives the twelve endpoints that exist today, but
// it is driven by hand-maintained case lists: a THIRTEENTH method added to
// sharedStackLifecycle, or a new exported handler written straight against
// h.sharedThanosPayload instead of through the driver, would be covered by
// nothing. Neither the build, go vet, nor the audit coverage contract (which
// only checks that recordAudit is reachable) would notice.
//
// These two tests need no list. They parse monitoring_stack_shared.go and
// enforce the two properties the file's doc comment claims:
//
//  1. every handler-shaped method on sharedStackLifecycle opens with
//     `if !l.h.authz.authorizeGlobalAction(w, r, rbac.ResourceMonitoring, …) { return }`
//  2. every exported handler-shaped method on *MonitoringHandler in that file
//     is a bare delegation to one of those, with no body of its own to leave a
//     check out of
//
// Together they make the case lists' completeness irrelevant: an endpoint can
// only be added to this family by adding a gated driver method.

const sharedStackLifecycleFile = "monitoring_stack_shared.go"

func parseSharedStackLifecycleFile(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), sharedStackLifecycleFile, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", sharedStackLifecycleFile, err)
	}
	return file
}

// receiverTypeName unwraps `*T`, `T[Req]` and `*T[Req]` down to `T`.
func receiverTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) != 1 {
		return ""
	}
	expr := recv.List[0].Type
	for {
		switch typed := expr.(type) {
		case *ast.StarExpr:
			expr = typed.X
		case *ast.IndexExpr:
			expr = typed.X
		case *ast.IndexListExpr:
			expr = typed.X
		case *ast.Ident:
			return typed.Name
		default:
			return ""
		}
	}
}

// isHTTPHandlerShape reports whether the signature is exactly
// (http.ResponseWriter, *http.Request) with no results — i.e. whether the
// method can be mounted on a route.
func isHTTPHandlerShape(fn *ast.FuncType) bool {
	if fn.Results != nil && len(fn.Results.List) > 0 {
		return false
	}
	if fn.Params == nil || len(fn.Params.List) != 2 {
		return false
	}
	if !isSelector(fn.Params.List[0].Type, "http", "ResponseWriter") {
		return false
	}
	star, ok := fn.Params.List[1].Type.(*ast.StarExpr)
	return ok && isSelector(star.X, "http", "Request")
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// TestSharedStackLifecycleMethodsOpenWithTheAuthorizationGate enforces property
// (1): no route-shaped method on the driver can exist without the preamble.
func TestSharedStackLifecycleMethodsOpenWithTheAuthorizationGate(t *testing.T) {
	file := parseSharedStackLifecycleFile(t)

	gated := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if receiverTypeName(fn.Recv) != "sharedStackLifecycle" || !isHTTPHandlerShape(fn.Type) {
			continue
		}
		gated++
		t.Run(fn.Name.Name, func(t *testing.T) {
			if len(fn.Body.List) == 0 {
				t.Fatalf("%s has an empty body; it must open with the authorization gate", fn.Name.Name)
			}
			ifStmt, ok := fn.Body.List[0].(*ast.IfStmt)
			if !ok || ifStmt.Init != nil {
				t.Fatalf("%s: first statement is %T, want `if !…authorizeGlobalAction(…) { return }`", fn.Name.Name, fn.Body.List[0])
			}
			unary, ok := ifStmt.Cond.(*ast.UnaryExpr)
			if !ok || unary.Op != token.NOT {
				t.Fatalf("%s: gate condition is not a negation", fn.Name.Name)
			}
			call, ok := unary.X.(*ast.CallExpr)
			if !ok {
				t.Fatalf("%s: gate condition is not a call", fn.Name.Name)
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "authorizeGlobalAction" {
				t.Fatalf("%s: first statement does not call authorizeGlobalAction", fn.Name.Name)
			}
			// (w, r, rbac.ResourceMonitoring, verb) — the resource is pinned,
			// the verb legitimately differs per method.
			if len(call.Args) != 4 || !isSelector(call.Args[2], "rbac", "ResourceMonitoring") {
				t.Fatalf("%s: gate does not check rbac.ResourceMonitoring", fn.Name.Name)
			}
			if len(ifStmt.Body.List) != 1 {
				t.Fatalf("%s: gate body has %d statements, want a single return", fn.Name.Name, len(ifStmt.Body.List))
			}
			if _, ok := ifStmt.Body.List[0].(*ast.ReturnStmt); !ok {
				t.Fatalf("%s: a failed gate does not return", fn.Name.Name)
			}
			if ifStmt.Else != nil {
				t.Fatalf("%s: gate has an else branch, so the denial is not unconditional", fn.Name.Name)
			}
		})
	}

	// A refactor that renames the type or moves the methods out must not turn
	// this test into a silent no-op.
	if gated < 6 {
		t.Fatalf("found %d gated sharedStackLifecycle methods, want at least the 6 lifecycle verbs — has the driver moved?", gated)
	}
}

// TestSharedStackHandlersOnlyDelegateToTheLifecycleDriver enforces property (2):
// the exported entry points carry no logic, so there is no second place an
// authorization check could have been meant to live.
func TestSharedStackHandlersOnlyDelegateToTheLifecycleDriver(t *testing.T) {
	file := parseSharedStackLifecycleFile(t)

	// The driver methods a handler is allowed to delegate to.
	driverMethods := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if receiverTypeName(fn.Recv) == "sharedStackLifecycle" && isHTTPHandlerShape(fn.Type) {
			driverMethods[fn.Name.Name] = true
		}
	}

	delegations := 0
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if receiverTypeName(fn.Recv) != "MonitoringHandler" || !fn.Name.IsExported() || !isHTTPHandlerShape(fn.Type) {
			continue
		}
		delegations++
		t.Run(fn.Name.Name, func(t *testing.T) {
			if len(fn.Body.List) != 1 {
				t.Fatalf("%s has %d statements; an exported handler in %s must be a single delegation to a gated lifecycle method",
					fn.Name.Name, len(fn.Body.List), sharedStackLifecycleFile)
			}
			expr, ok := fn.Body.List[0].(*ast.ExprStmt)
			if !ok {
				t.Fatalf("%s: body is %T, want a delegating call", fn.Name.Name, fn.Body.List[0])
			}
			call, ok := expr.X.(*ast.CallExpr)
			if !ok {
				t.Fatalf("%s: body is not a call", fn.Name.Name)
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !driverMethods[sel.Sel.Name] {
				t.Fatalf("%s does not delegate to a gated sharedStackLifecycle method (got %v)", fn.Name.Name, call.Fun)
			}
			// The receiver of that call must itself be a call — the
			// h.…Lifecycle() constructor — not a stashed value.
			if _, ok := sel.X.(*ast.CallExpr); !ok {
				t.Fatalf("%s: delegation target is not an inline lifecycle constructor", fn.Name.Name)
			}
		})
	}

	if delegations != 18 {
		t.Fatalf("found %d exported shared-stack handlers, want 18 (three families x six endpoints); "+
			"if an endpoint was legitimately added or removed, update this count deliberately", delegations)
	}
}
