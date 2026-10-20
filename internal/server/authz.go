package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/alphabravocompany/astronomer-go/internal/api/apiutil"
	"github.com/alphabravocompany/astronomer-go/internal/audit"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/alphabravocompany/astronomer-go/internal/reqctx"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
)

// Scope selects how a Permission's (cluster, project, namespace) scope — and
// for the Kubernetes-facing routes the resource itself — is resolved from the
// request. Every value reproduces exactly one of the gate shapes the routes
// mounted before the table existed (ADR-0012).
type Scope int

const (
	// ScopeRoute evaluates Resource/Verb at {cluster_id}/{project_id} (a bare
	// {id} counts for the clusters/projects resources, or for any resource when
	// IDNamesCluster is set) and the namespace route param only.
	ScopeRoute Scope = iota
	// ScopeQueryNamespace is ScopeRoute plus the ?namespace= fallback, for
	// handlers that narrow their own upstream query to that same value.
	ScopeQueryNamespace
	// ScopeCollection admits a caller holding the permission on any cluster or
	// project; the handler filters the page.
	ScopeCollection
	// ScopeNamespaceList admits a namespace-scoped reader to a bare list when
	// namespace-scoped RBAC is enabled; the handler filters.
	ScopeNamespaceList
	// ScopeAnyOf / ScopeAllOf evaluate Requirements at {cluster_id}/{project_id}.
	ScopeAnyOf
	ScopeAllOf
	// ScopeNamespacePicker gates the namespace/event pickers (clusters:read,
	// or any namespace read on workloads/pods when namespace-scoped RBAC is on).
	ScopeNamespacePicker
	// ScopeK8sProxy derives resource/verb from the proxied Kubernetes path and
	// consults the native per-CRD authorizer.
	ScopeK8sProxy
	// ScopeNamedResource derives the resource from the RouteParam route param.
	ScopeNamedResource
	// ScopeNamedResourceForce additionally requires <resource>:manage when the
	// request asks server-side apply to force field ownership (?force=true).
	ScopeNamedResourceForce
	// ScopeNamedResourceList / ScopeGenericResourceList derive the resource
	// from {resource_type} and honour ?namespace=.
	ScopeNamedResourceList
	ScopeGenericResourceList
	// ScopeNamedResourceCreate authorizes against metadata.namespace in the
	// request body, the namespace the handler will write to.
	ScopeNamedResourceCreate
)

// Requirement is one resource/verb pair of an AnyOf/AllOf permission.
type Requirement struct {
	Resource rbac.Resource
	Verb     rbac.Verb
}

// Permission is one row of the static route → permission table.
type Permission struct {
	// Public routes are not user-authenticated (or authenticate a non-user
	// principal such as a SCIM token); the authorizer never sees them.
	Public bool
	// Authenticated admits any authenticated user; the route carries no RBAC
	// requirement of its own.
	Authenticated bool
	// Superuser admits only platform superusers.
	Superuser bool

	Resource     rbac.Resource
	Verb         rbac.Verb
	Scope        Scope
	RouteParam   string
	Requirements []Requirement

	// IDNamesCluster declares that a bare {id} in this route names a cluster.
	IDNamesCluster bool
	// DeliveryProject resolves the delivery API's query/header/body project
	// scope into {project_id} before evaluation.
	DeliveryProject bool

	// Also lists further permissions that must all hold (evaluated in order).
	Also []Permission
}

// String renders the requirement for audit records and logs.
func (p Permission) String() string {
	var b strings.Builder
	switch {
	case p.Public:
		return "public"
	case p.Authenticated:
		return "authenticated"
	case p.Superuser:
		b.WriteString("superuser")
	case len(p.Requirements) > 0:
		parts := make([]string, 0, len(p.Requirements))
		for _, req := range p.Requirements {
			parts = append(parts, string(req.Resource)+":"+string(req.Verb))
		}
		sep := " | "
		if p.Scope == ScopeAllOf {
			sep = " & "
		}
		b.WriteString(strings.Join(parts, sep))
	case p.Resource != "":
		b.WriteString(string(p.Resource) + ":" + string(p.Verb))
	default:
		b.WriteString(scopeNames[p.Scope])
		if p.RouteParam != "" {
			b.WriteString("{" + p.RouteParam + "}")
		}
		if p.Verb != "" {
			b.WriteString(":" + string(p.Verb))
		}
	}
	for _, also := range p.Also {
		b.WriteString(" & " + also.String())
	}
	return b.String()
}

var scopeNames = map[Scope]string{
	ScopeRoute: "route", ScopeQueryNamespace: "query_namespace", ScopeCollection: "collection",
	ScopeNamespaceList: "namespace_list", ScopeAnyOf: "any_of", ScopeAllOf: "all_of",
	ScopeNamespacePicker: "namespace_picker", ScopeK8sProxy: "k8s_proxy", ScopeNamedResource: "named_resource",
	ScopeNamedResourceForce: "named_resource_force", ScopeNamedResourceList: "named_resource_list",
	ScopeGenericResourceList: "generic_resource_list", ScopeNamedResourceCreate: "named_resource_create",
}

// routeKey is the table key: METHOD + " " + the chi route pattern with the
// "/*/" joints of mounted sub-routers collapsed.
func routeKey(method, pattern string) string {
	for strings.Contains(pattern, "/*/") {
		pattern = strings.ReplaceAll(pattern, "/*/", "/")
	}
	return method + " " + pattern
}

// routeAuthorizer is THE authorization middleware (guide ch.06). It runs
// immediately after every authenticator (see requireAuth), resolves the
// request's chi route pattern against the root router, looks the route up in
// routePermissions, denies any authenticated route that has no row, and
// evaluates the row with the rbac engine. Every denial is logged and written
// to the audit log with the subject, route key, required permission and the
// subject's actual roles.
type routeAuthorizer struct {
	root   *chi.Mux
	table  map[string]Permission
	gates  map[string]func(http.Handler) http.Handler
	deps   RouterDependencies
	logger *slog.Logger
}

func newRouteAuthorizer(root *chi.Mux, deps RouterDependencies, table map[string]Permission) *routeAuthorizer {
	a := &routeAuthorizer{root: root, table: table, deps: deps, logger: slog.Default(), gates: make(map[string]func(http.Handler) http.Handler, len(table))}
	for key, perm := range table {
		if perm.Public || perm.Authenticated {
			continue
		}
		a.gates[key] = a.gate(perm)
	}
	return a
}

// after composes authn with the authorizer so authorization always runs
// immediately after authentication succeeds.
func (a *routeAuthorizer) after(authn func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return authn(a.authorize(next))
	}
}

func (a *routeAuthorizer) authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, resolved, ok := a.resolve(r)
		if !ok {
			// No route matches: the router answers 404/405 and no handler runs.
			next.ServeHTTP(w, r)
			return
		}
		perm, listed := a.table[key]
		switch {
		case !listed || perm.Public:
			// Deny by default. A Public row reached through an authenticator is
			// a table/router disagreement and fails closed too.
			a.deny(w, r, key, "unlisted")
			return
		case perm.Authenticated:
			next.ServeHTTP(w, r)
			return
		}
		a.enforce(key, perm, a.gates[key], resolved, next).ServeHTTP(w, r)
	})
}

// enforce runs one row's evaluator chain against r (with routeCtx bound for
// URL-param scope resolution), audits a 403, and on admission continues with
// the admitted request under the router's own route context.
func (a *routeAuthorizer) enforce(key string, perm Permission, gate func(http.Handler) http.Handler, routeCtx *chi.Context, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		original := chi.RouteContext(r.Context())
		gateRequest := r
		if routeCtx != nil {
			gateRequest = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, routeCtx))
		}
		var admitted *http.Request
		recorder := &statusRecorder{ResponseWriter: w}
		gate(http.HandlerFunc(func(_ http.ResponseWriter, passed *http.Request) {
			admitted = passed
		})).ServeHTTP(recorder, gateRequest)
		if admitted == nil {
			if recorder.status == http.StatusForbidden {
				a.recordDenial(r, key, perm.String())
			}
			return
		}
		// Continue with the request the gates admitted (a gate may restore a
		// buffered body or attach context) under the router's own route context.
		next.ServeHTTP(w, admitted.WithContext(context.WithValue(admitted.Context(), chi.RouteCtxKey, original)))
	})
}

// enforceFixed authorizes a handler chain that is not dispatched by the root
// router (the isolated tool-UI hosts) against a fixed row of toolUIPermissions.
func (a *routeAuthorizer) enforceFixed(key string, next http.Handler) http.Handler {
	perm, ok := toolUIPermissions[key]
	if !ok {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { a.deny(w, r, key, "unlisted") })
	}
	return a.enforce(key, perm, a.gate(perm), nil, next)
}

// resolve finds the route the root router will dispatch r to, with all URL
// params bound, without disturbing the in-flight routing state.
func (a *routeAuthorizer) resolve(r *http.Request) (string, *chi.Context, bool) {
	if a.root == nil {
		return "", nil, false
	}
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	tctx := chi.NewRouteContext()
	pattern := a.root.Find(tctx, r.Method, path)
	if pattern == "" {
		return "", nil, false
	}
	return routeKey(r.Method, pattern), tctx, true
}

func (a *routeAuthorizer) deny(w http.ResponseWriter, r *http.Request, key, required string) {
	apiutil.WriteError(w, r, http.StatusForbidden, apiutil.PermissionDenied, "You do not have permission to perform this action")
	a.recordDenial(r, key, required)
}

// recordDenial emits the structured denial log line and the audit record.
func (a *routeAuthorizer) recordDenial(r *http.Request, key, required string) {
	ctx := r.Context()
	subject := ""
	authMethod := ""
	if user, ok := reqctx.AuthenticatedUser(ctx); ok && user != nil {
		subject, authMethod = user.ID, user.AuthMethod
	}
	roles := a.actualRoles(ctx, subject)
	a.logger.WarnContext(ctx, "authorization denied",
		"event", "authz_denied",
		"sub", subject,
		"route", key,
		"required", required,
		"roles", roles,
		"request_id", reqctx.RequestID(ctx),
	)
	writer := a.deps.CoreAuth.AuditWriter
	if writer == nil && a.deps.CoreAuth.Queries != nil {
		writer = a.deps.CoreAuth.Queries
	}
	if writer == nil {
		return
	}
	audit.Record(ctx, writer, audit.NewHTTPRequestEvent(audit.HTTPRequestEvent{
		Request: r, Source: "http", CorrelationID: reqctx.CorrelationID(ctx),
		UserID: reqctx.UserUUID(ctx), ActorAuthMethod: authMethod,
		Action: "auth.authorization_denied", ResourceType: "route", ResourceID: key,
		StatusCode: http.StatusForbidden, RequestID: reqctx.RequestID(ctx), IPAddress: reqctx.ClientIP(r),
		Detail: map[string]any{"route": key, "required": required, "roles": roles},
	}))
}

func (a *routeAuthorizer) actualRoles(ctx context.Context, subject string) []string {
	roles := []string{}
	querier := a.deps.CoreAuth.RBACQueries
	if subject == "" || querier == nil {
		return roles
	}
	bindings, err := querier.GetUserBindings(ctx, subject)
	if err != nil {
		return roles
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		name := b.RoleName
		if b.IsSuperuser {
			name = "superuser"
		}
		if name == "" {
			continue
		}
		label := name
		if b.Scope != "" {
			label = b.Scope + ":" + name
		}
		if !seen[label] {
			seen[label] = true
			roles = append(roles, label)
		}
	}
	return roles
}

// gate builds the evaluator chain for one table row from the rbac engine.
func (a *routeAuthorizer) gate(p Permission) func(http.Handler) http.Handler {
	chain := []func(http.Handler) http.Handler{a.single(p)}
	for _, also := range p.Also {
		chain = append(chain, a.single(also))
	}
	return func(next http.Handler) http.Handler {
		for i := len(chain) - 1; i >= 0; i-- {
			next = chain[i](next)
		}
		return next
	}
}

func (a *routeAuthorizer) single(p Permission) func(http.Handler) http.Handler {
	engine, querier := a.deps.CoreAuth.RBACEngine, a.deps.CoreAuth.RBACQueries
	nsScoped := a.deps.ClusterResources.NamespaceScopedRBAC
	var eval func(http.Handler) http.Handler
	switch {
	case p.Superuser:
		eval = requireSuperuser(a.deps)
	case p.Scope == ScopeRoute:
		eval = requirePermission(engine, querier, p.Resource, p.Verb)
	case p.Scope == ScopeQueryNamespace:
		eval = requireQueryNamespacePermission(engine, querier, p.Resource, p.Verb)
	case p.Scope == ScopeCollection:
		eval = requireCollectionPermission(engine, querier, p.Resource, p.Verb)
	case p.Scope == ScopeNamespaceList:
		eval = requireListPermission(engine, querier, p.Resource, p.Verb, nsScoped)
	case p.Scope == ScopeAnyOf:
		eval = requireAnyPermission(engine, querier, p.Requirements...)
	case p.Scope == ScopeAllOf:
		eval = requireAllPermissions(engine, querier, p.Requirements...)
	case p.Scope == ScopeNamespacePicker:
		eval = requireNamespacePickerListPermission(engine, querier, nsScoped)
	case p.Scope == ScopeK8sProxy:
		eval = requireK8sProxyPermission(engine, querier, a.deps.ClusterResources.NativeAuthz, nsScoped)
	case p.Scope == ScopeNamedResource:
		eval = requireNamedResourcePermission(engine, querier, p.RouteParam, p.Verb)
	case p.Scope == ScopeNamedResourceForce:
		eval = requireNamedResourceForcePermission(engine, querier, p.RouteParam)
	case p.Scope == ScopeNamedResourceList:
		eval = requireNamedResourceListPermission(engine, querier)
	case p.Scope == ScopeGenericResourceList:
		eval = requireGenericResourceListPermission(engine, querier)
	case p.Scope == ScopeNamedResourceCreate:
		eval = requireNamedResourceCreatePermission(engine, querier)
	default:
		eval = unavailableSecurityDependency("RBAC authorization")
	}
	if p.IDNamesCluster {
		inner := eval
		eval = func(next http.Handler) http.Handler { return appmiddleware.ClusterScopeFromIDParam(inner(next)) }
	}
	if p.DeliveryProject {
		inner := eval
		eval = func(next http.Handler) http.Handler { return deliveryProjectScope(inner(next)) }
	}
	return eval
}

// statusRecorder observes the status a gate wrote.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}
