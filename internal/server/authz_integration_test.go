//go:build integration

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
)

// authorizerMiddlewareName identifies the authn+authz composition in a chi
// middleware chain.
const authorizerMiddlewareName = "(*routeAuthorizer).after.func1"

type walkedRoute struct {
	authorized bool
}

func walkRoutes(t *testing.T, router chi.Routes, into map[string]walkedRoute) {
	t.Helper()
	if err := chi.Walk(router, func(method, route string, _ http.Handler, mws ...func(http.Handler) http.Handler) error {
		authorized := false
		for _, mw := range mws {
			if strings.HasSuffix(runtime.FuncForPC(reflect.ValueOf(mw).Pointer()).Name(), authorizerMiddlewareName) {
				authorized = true
			}
		}
		key := routeKey(method, route)
		if prev, seen := into[key]; seen && prev.authorized != authorized {
			t.Errorf("%s: authorizer presence differs between routers", key)
		}
		into[key] = walkedRoute{authorized: authorized}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRoutePermissionTableMatchesRouter enumerates every route the production
// router registers (all optional families on) plus every route of the
// fully-wired route-security router, and requires a one-to-one match with
// routePermissions: no route without a row, no stale row, and the authorizer
// in the chain of exactly the non-public rows.
func TestRoutePermissionTableMatchesRouter(t *testing.T) {
	t.Parallel()
	h := newProductionRouterHarness(t)
	routes := map[string]walkedRoute{}
	walkRoutes(t, h.router, routes)
	secDeps, _ := routeSecurityRouterDependencies(t)
	walkRoutes(t, NewRouter(&config.Config{}, secDeps), routes)

	var missing, stale, mismatched []string
	for key, route := range routes {
		perm, ok := routePermissions[key]
		if !ok {
			missing = append(missing, key)
			continue
		}
		if perm.Public == route.authorized {
			mismatched = append(mismatched, key)
		}
	}
	for key := range routePermissions {
		if _, ok := routes[key]; !ok {
			stale = append(stale, key)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(mismatched)
	if len(missing)+len(stale)+len(mismatched) > 0 {
		t.Fatalf("route table out of sync with the router\nmissing rows:\n  %s\nstale rows:\n  %s\npublic flag disagrees with the chain:\n  %s",
			strings.Join(missing, "\n  "), strings.Join(stale, "\n  "), strings.Join(mismatched, "\n  "))
	}
	t.Logf("%d routes, %d table rows, all matched", len(routes), len(routePermissions))
}

var routeParamPattern = regexp.MustCompile(`\{([A-Za-z_]+)(:[^}]*)?\}`)

// concretePath fills a route pattern with values that satisfy every gate's
// parser: UUIDs for ids, "default" for namespaces, a ConfigMap type for the
// typed-resource routes and a canonical namespaced path for the k8s proxy.
func concretePath(pattern string, id string) string {
	path := routeParamPattern.ReplaceAllStringFunc(pattern, func(m string) string {
		sub := routeParamPattern.FindStringSubmatch(m)
		name, constraint := sub[1], strings.TrimPrefix(sub[2], ":")
		if alt := strings.TrimSuffix(strings.TrimPrefix(constraint, "(?:"), ")"); alt != constraint {
			return strings.Split(alt, "|")[0]
		}
		lower := strings.ToLower(name)
		switch {
		case strings.Contains(lower, "namespace") || lower == "ns":
			return "default"
		case lower == "resource_type" || lower == "type":
			return "configmaps"
		case strings.HasSuffix(lower, "id") || lower == "id":
			return id
		default:
			return "x1"
		}
	})
	if strings.HasSuffix(path, "/k8s/*") {
		return strings.TrimSuffix(path, "*") + "api/v1/namespaces/default/configmaps"
	}
	return strings.TrimSuffix(path, "*") + map[bool]string{true: "x1"}[strings.HasSuffix(path, "*")]
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// readOnlySatisfies reports whether a read/list/watch-everything role can hold
// every verb the row requires for a mutating request.
func readOnlySatisfies(p Permission) bool {
	readish := func(v rbac.Verb) bool { return v == rbac.VerbRead || v == rbac.VerbList || v == rbac.VerbWatch }
	ok := !p.Superuser
	switch p.Scope {
	case ScopeAnyOf:
		any := false
		for _, r := range p.Requirements {
			any = any || readish(r.Verb)
		}
		ok = ok && any
	case ScopeAllOf:
		for _, r := range p.Requirements {
			ok = ok && readish(r.Verb)
		}
	case ScopeK8sProxy, ScopeNamedResourceCreate, ScopeNamedResourceForce:
		ok = false
	case ScopeNamedResource:
		ok = ok && readish(p.Verb)
	default:
		ok = ok && readish(p.Verb)
	}
	for _, also := range p.Also {
		ok = ok && readOnlySatisfies(also)
	}
	return ok
}

func mutationRequest(method, key string) *http.Request {
	pattern := strings.SplitN(key, " ", 2)[1]
	req := httptest.NewRequest(method, concretePath(pattern, uuid.NewString()), strings.NewReader(`{"metadata":{"namespace":"default"}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Project-ID", uuid.NewString())
	return req
}

// TestRouteAuthorizationReadOnlyAndAdmin drives the table against the real
// production router and database:
//
//   - a read-only user (global role: every resource, read/list/watch) gets
//     403 permission_denied from EVERY mutating route whose row a read role
//     cannot satisfy, through the full router, and each denial leaves one
//     auth.authorization_denied audit row naming the route;
//   - the same user gets 200 from the core list endpoints;
//   - a superuser is admitted by the authorizer on every non-public row.
func TestRouteAuthorizationReadOnlyAndAdmin(t *testing.T) {
	t.Parallel()
	h := newProductionRouterHarness(t)
	readOnlyID, readOnlyToken := h.user(t, "authz-readonly", false, []map[string]any{{"resource": "*", "verbs": []string{"read", "list", "watch"}}})
	adminID, adminToken := h.user(t, "authz-admin", true, nil)

	routes := map[string]walkedRoute{}
	walkRoutes(t, h.router, routes)

	denied := map[string]bool{}
	for key := range routes {
		perm := routePermissions[key]
		method := strings.SplitN(key, " ", 2)[0]
		if perm.Public || perm.Authenticated || !isMutating(method) || readOnlySatisfies(perm) {
			continue
		}
		rec := h.do(t, mutationRequest(method, key), readOnlyToken)
		var body apiutil.Body
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != http.StatusForbidden || body.Error.Code != apiutil.PermissionDenied {
			t.Errorf("%s as read-only: status %d code %q, want 403 permission_denied (body %s)", key, rec.Code, body.Error.Code, rec.Body.String())
			continue
		}
		denied[key] = true
	}
	if len(denied) < 240 {
		t.Fatalf("only %d mutating routes were exercised; the table should yield several hundred", len(denied))
	}

	rows, err := h.pool.Query(context.Background(),
		`SELECT resource_id, detail FROM audit_log WHERE action = 'auth.authorization_denied' AND user_id = $1`, readOnlyID)
	if err != nil {
		t.Fatal(err)
	}
	audited := map[string]int{}
	for rows.Next() {
		var routeID string
		var detail map[string]any
		if err := rows.Scan(&routeID, &detail); err != nil {
			t.Fatal(err)
		}
		audited[routeID]++
		if detail["required"] == "" || detail["route"] != routeID {
			t.Errorf("audit detail for %s incomplete: %v", routeID, detail)
		}
		roles, _ := detail["roles"].([]any)
		if len(roles) != 1 || roles[0] != "global:authz-readonly-role" {
			t.Errorf("audit detail for %s has roles %v, want [global:authz-readonly-role]", routeID, roles)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for key := range denied {
		if audited[key] != 1 {
			t.Errorf("%s: %d audit rows for one denial", key, audited[key])
		}
	}
	t.Logf("read-only: %d mutating routes denied, %d audit rows", len(denied), len(audited))

	for _, path := range []string{"/api/v1/clusters/", "/api/v1/projects/", "/api/v1/users/", "/api/v1/rbac/global-roles/", "/api/v1/settings/audit-logs/", "/api/v1/auth/me/"} {
		rec := h.do(t, httptest.NewRequest(http.MethodGet, path, nil), readOnlyToken)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s as read-only: %d %s", path, rec.Code, rec.Body.String())
		}
	}

	// The superuser is admitted on every non-public row. Evaluated through
	// the authorizer against the real router and RBAC store, without running
	// handlers (several would reach Kubernetes or the network).
	authorizer := newRouteAuthorizer(h.router.(*chi.Mux), h.deps, routePermissions)
	admitted := 0
	for key := range routes {
		perm := routePermissions[key]
		if perm.Public {
			continue
		}
		method := strings.SplitN(key, " ", 2)[0]
		req := mutationRequest(method, key)
		req = req.WithContext(reqctx.WithUser(req.Context(), &reqctx.User{ID: adminID.String(), AuthMethod: "jwt"}))
		reached := false
		rec := httptest.NewRecorder()
		authorizer.authorize(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })).ServeHTTP(rec, req)
		if !reached {
			t.Errorf("%s: superuser not admitted (%d %s)", key, rec.Code, rec.Body.String())
			continue
		}
		admitted++
	}
	t.Logf("superuser admitted on %d non-public routes", admitted)

	// And end to end: the superuser's mutation reaches the handler.
	rec := h.do(t, httptest.NewRequest(http.MethodPost, "/api/v1/rbac/global-roles/", strings.NewReader(`{"name":"authz-created","display_name":"x","rules":[]}`)), adminToken)
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Fatalf("superuser POST /rbac/global-roles/ denied: %d %s", rec.Code, rec.Body.String())
	}
}
