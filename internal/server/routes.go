package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alphabravocompany/astronomer-go/internal/reqctx"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/google/uuid"

	"github.com/alphabravocompany/astronomer-go/internal/audit"
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/alphabravocompany/astronomer-go/internal/tunnel"
)

const charlieAdminReconciliationTimeout = 7 * time.Minute

func longRunningCharlieAdminMutation(method, path string) bool {
	path = strings.TrimSuffix(path, "/")
	if method == http.MethodPut && path == "/api/v1/admin/charlie/kubernetes-visibility" {
		return true
	}
	if method != http.MethodPost && method != http.MethodPatch {
		return false
	}
	switch path {
	case "/api/v1/admin/charlie/onboarding/consume",
		"/api/v1/admin/charlie/disconnect",
		"/api/v1/admin/charlie/mode":
		return true
	default:
		return false
	}
}

func apiRequestTimeout(duration time.Duration) func(http.Handler) http.Handler {
	bounded := chimiddleware.Timeout(duration)
	reconciliation := chimiddleware.Timeout(charlieAdminReconciliationTimeout)
	return func(next http.Handler) http.Handler {
		timed := bounded(next)
		longTimed := reconciliation(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := strings.TrimSuffix(r.URL.Path, "/")
			if r.Method == http.MethodGet && strings.HasPrefix(path, "/api/v1/charlie/sessions/") && strings.HasSuffix(path, "/events") {
				next.ServeHTTP(w, r)
				return
			}
			// Charlie installation, replacement, Kubernetes visibility, and mode
			// transitions synchronously verify a delivery reconciliation and a
			// two-replica StatefulSet rollout. The ordinary REST deadline can
			// expire after Kubernetes accepted the least-authority ceiling but
			// before the audited database transition commits. Keep these exact
			// administrator mutations bounded, but give the configured five-minute
			// rollout enough time plus a final bridge readback. Every operation is
			// revision-checked and idempotent, so a disconnected client can retry.
			if longRunningCharlieAdminMutation(r.Method, path) {
				longTimed.ServeHTTP(w, r)
				return
			}
			timed.ServeHTTP(w, r)
		})
	}
}

// NewProductionRouter validates every security-critical production dependency
// before any route or middleware is constructed.
func NewProductionRouter(cfg *config.Config, deps RouterDependencies) (chi.Router, error) {
	if err := validateProductionSecurityWiring(cfg, deps); err != nil {
		return nil, err
	}
	return NewRouter(cfg, deps), nil
}

// NewRouter builds the Chi router. It accepts partial dependency groups for
// focused tests; every missing security dependency still fails closed.
func NewRouter(cfg *config.Config, deps RouterDependencies) chi.Router {
	r := chi.NewRouter()

	// Per-endpoint-class rate limiter. Bucket store
	// lives for the lifetime of the process; the janitor inside cleans up
	// idle buckets so the map doesn't leak (same pattern as the login
	// limiter). One limiter shared across all four classes so
	// chart-tuned configs apply uniformly.
	rateLimitCtx := context.Background()
	rateLimiter := appmiddleware.NewAPIRateLimiter(rateLimitCtx, appmiddleware.APIRateLimitConfigs(map[appmiddleware.APIRateLimitClass]appmiddleware.APIRateLimitConfig{
		appmiddleware.ClassK8sProxy: {
			RatePerSecond: cfg.APIK8sProxyRateLimitRPS,
			Burst:         cfg.APIK8sProxyRateLimitBurst,
		},
	}))
	rateLimit := func(class appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler {
		return rateLimiter.Middleware(class)
	}

	// Middleware
	r.Use(appmiddleware.RequestID)
	r.Use(appmiddleware.TrustedRealIP(cfg.TrustedProxyCIDRs))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			next.ServeHTTP(w, request.WithContext(reqctx.WithSecureCookiesRequired(request.Context(), config.IsProduction(cfg))))
		})
	})
	r.Use(appmiddleware.SecurityHeaders)
	r.Use(appmiddleware.RequestLogger)
	r.Use(chimiddleware.Recoverer)
	r.Use(appmiddleware.Metrics)
	// Keep the server-level ReadTimeout at zero for WebSockets/SSE while still
	// bounding every REST/SCIM mutation body by bytes and socket read time.
	r.Use(appmiddleware.BoundRequestBodies(appmiddleware.DefaultMaxRequestBodyBytes, appmiddleware.DefaultRequestBodyTimeout))
	// Normalise `/api/v1/foo` → `/api/v1/foo/` before chi matches so
	// the frontend's no-trailing-slash REST calls hit the same route
	// the trailing-slash form does. Without this, DELETE /clusters/{id}
	// 404s because the route is mounted as /{id}/ — the user-facing
	// symptom is "the cluster delete button in the UI silently fails."
	// Scoped to /api/v1/* so static helm-repo assets are not
	// affected.
	r.Use(appmiddleware.NormalizeAPITrailingSlash)
	// Rename the otelhttp server span to use chi's route pattern
	// once routing has run. otelhttp.NewHandler wraps the router with
	// only the HTTP method as a placeholder span name; this middleware
	// upgrades it to "METHOD /api/v1/path/{id}" so traces aggregate by
	// route instead of by raw URL.
	r.Use(chiRoutePatternSpanName)
	// NOTE: chimiddleware.Timeout is applied per-group below — it MUST NOT be
	// applied globally because /api/v1/ws/... carries long-lived WebSocket
	// connections that would otherwise be force-closed at the timeout.

	// CORS
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins(),
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Request-ID"},
		ExposedHeaders:   []string{"Link", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	registerPublicRoutes(r, cfg, deps, rateLimit)
	// API v1
	r.Route("/api/v1", func(r chi.Router) {
		// REST-only timeout. Charlie's authenticated event stream is explicitly
		// exempt because chi's timeout writer cannot expose http.Flusher and
		// would both break SSE and terminate healthy turns at 30 seconds.
		r.Use(apiRequestTimeout(30 * time.Second))
		// /bootstrap/ and /bootstrap/complete/ were removed when the server
		// switched to the Rancher-style admin-on-first-boot model: the
		// startup hook in cmd/server/main.go (auth.EnsureBootstrapAdmin)
		// creates the admin user. No HTTP endpoint is needed for platform
		// first-setup any more.

		registerAPIEntryRoutes(r, cfg, deps)
		authenticated := chi.NewRouter()
		if deps.CoreAuth.Queries != nil {
			// Authentication failures return before authenticated middleware
			// can observe the request. Record only Charlie's unauthenticated
			// mutation denials here, using its content-free audit contract.
			authenticated.Use(appmiddleware.CharlieAuthenticationDenialAuditWithWriter(slog.Default(), deps.CoreAuth.Queries))
		}
		authenticated.Use(requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries))
		if deps.CoreAuth.Queries != nil {
			// Keep the normal mutation auditor immediately after auth so it
			// receives actor context and still observes write-scope/RBAC denials.
			authenticated.Use(appmiddleware.AuditLogWithWriter(slog.Default(), deps.CoreAuth.Queries))
		}
		// Default-deny scope backstop: a read-only API token can never
		// reach a mutating handler, regardless of whether the specific
		// subtree opted into a write scope. Wired right after auth so
		// the token row is in context. `required=""` keeps this purely
		// a read-only-token rejector — subtree-level
		// RequireWriteScopeForMutations / requireScope still enforce the
		// specific write scope on top, and RBAC remains the primary gate.
		// GET/HEAD/OPTIONS, JWT sessions, and legacy empty-scope tokens
		// pass through untouched (see RequireWriteScopeForMutations).
		authenticated.Use(appmiddleware.RequireWriteScopeForMutations(""))
		// Migration 063 — read-side audit. Wire AFTER auth so we
		// know the actor, and BEFORE per-route handlers so the
		// middleware sees every authenticated read. Nil-safe: when
		// the evaluator or DB writer is unwired the middleware is
		// simply not attached.
		if deps.CoreAuth.ReadAuditEvaluator != nil && deps.CoreAuth.Queries != nil {
			authenticated.Use(appmiddleware.ReadAudit(deps.CoreAuth.ReadAuditEvaluator, deps.CoreAuth.Queries))
		}
		r.Mount("/", authenticated)

		registerProtectedRoutes(authenticated, cfg, deps, rateLimit)
	})

	registerLongLivedRoutes(r, deps, rateLimit)
	return r
}

func requireAuth(jwt *iauth.JWTManager, queries iauth.TokenUserQuerier) func(http.Handler) http.Handler {
	if jwt == nil {
		return unavailableSecurityDependency("JWT authentication")
	}
	return appmiddleware.RequireAuthWithQueries(jwt, queries)
}

func unavailableSecurityDependency(name string) func(http.Handler) http.Handler {
	return func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeRouteAuthError(w, http.StatusServiceUnavailable, "security_dependency_unavailable", name+" is unavailable")
		})
	}
}

// enrollChallengeOrAuth guards a route with either a normal session or a
// PurposeTOTPEnrollOnly challenge (see AuthOrTOTPEnrollChallenge). Mirrors
// requireAuth's nil-jwt passthrough for test wiring.
func enrollChallengeOrAuth(jwt *iauth.JWTManager, queries iauth.TokenUserQuerier) func(http.Handler) http.Handler {
	if jwt == nil {
		return unavailableSecurityDependency("JWT authentication")
	}
	return appmiddleware.AuthOrTOTPEnrollChallenge(jwt, queries)
}

// requireScope returns the API-token scope-enforcement middleware
// configured for `scope`. JWT sessions bypass the check; legacy
// (pre-044, empty-`scopes`) tokens are allowed through. See
// `APITokenScopeEnforce` for the full semantics.
func requireScope(scope string) func(http.Handler) http.Handler {
	return appmiddleware.APITokenScopeEnforce(scope)
}

// featureGate wraps the shared registry-backed middleware. A nil cache uses
// the feature's registered default; unknown keys still fail closed. This keeps
// fresh-install/test wiring aligned with the platform settings contract rather
// than inventing a second set of route defaults here.
func featureGate(key string, cache *handler.SettingsCache) func(http.Handler) http.Handler {
	return appmiddleware.FeatureGate(key, cache)
}

func requirePermission(engine *rbac.Engine, querier rbac.BindingQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return appmiddleware.RequirePermission(engine, querier, resource, verb)
}

// requireQueryNamespacePermission is requirePermission for a route whose
// handler narrows its own upstream query to ?namespace=. Gate namespace ==
// handler namespace, so naming one can only shrink the result set. Everything
// else must use requirePermission, which ignores the query and therefore fails
// closed for a namespace-narrowed caller. See
// appmiddleware.RequireQueryNamespacePermission.
func requireQueryNamespacePermission(engine *rbac.Engine, querier rbac.BindingQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return appmiddleware.RequireQueryNamespacePermission(engine, querier, resource, verb)
}

// requireCollectionPermission gates a top-level collection route (GET
// /clusters/, GET /projects/), admitting callers whose grant is cluster- or
// project-scoped instead of global. The handler behind it filters the page —
// see the RequireCollectionPermission doc for why the gate alone is not enough.
func requireCollectionPermission(engine *rbac.Engine, querier rbac.BindingQuerier, resource rbac.Resource, verb rbac.Verb) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return appmiddleware.RequireCollectionPermission(engine, querier, resource, verb)
}

type permissionRequirement struct {
	resource rbac.Resource
	verb     rbac.Verb
}

func requireAnyPermission(engine *rbac.Engine, querier rbac.BindingQuerier, requirements ...permissionRequirement) func(http.Handler) http.Handler {
	if engine == nil || querier == nil || len(requirements) == 0 {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := reqctx.AuthenticatedUser(r.Context())
			if !ok || user == nil {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			for _, requirement := range requirements {
				if engine.CheckPermission(bindings, requirement.resource, requirement.verb, clusterID, projectID) {
					next.ServeHTTP(w, r)
					return
				}
			}
			writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
		})
	}
}

func requireAllPermissions(engine *rbac.Engine, querier rbac.BindingQuerier, requirements ...permissionRequirement) func(http.Handler) http.Handler {
	if engine == nil || querier == nil || len(requirements) == 0 {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := reqctx.AuthenticatedUser(r.Context())
			if !ok || user == nil {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			for _, requirement := range requirements {
				if !engine.CheckPermission(bindings, requirement.resource, requirement.verb, clusterID, projectID) {
					writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// permissionScopeIDs resolves only unambiguous scope parameters. Callers that
// use this helper are mounted on {cluster_id}/{project_id} routes or on global
// collections. A generic {id} may name an alert, user, role, or other object and
// must never be reinterpreted as an authorization scope.
func permissionScopeIDs(r *http.Request) (uuid.UUID, uuid.UUID) {
	clusterID, _ := reqctx.ClusterID(r)
	projectID, _ := reqctx.ProjectID(r)
	return clusterID, projectID
}

// nativeNamespaceLister is an OPTIONAL capability a nativeAuthorizer may
// implement (via a type assertion) to participate in the cluster-wide-list
// allow-set filter, not just the single-namespace Allow() check. It returns the
// namespace visibility the user's native per-CRD rules grant for a cluster-wide
// LIST of (apiGroup, resource, verb) on clusterID:
//
//   - all==true  → a native rule grants this list without namespace narrowing
//     (any namespace). names must be ignored.
//   - all==false → names is the exact allow-set of namespaces the native rules
//     grant for this list (possibly empty → contributes nothing).
//
// Implementations MUST apply the same conservative guards as rbac.NativeAllow
// (refuse privilege-escalation api groups; native rules never widen exec/logs),
// so folding these namespaces into the list filter can never grant more than an
// operator explicitly authored. *nativeRBACAuthorizer implements this.
type nativeNamespaceLister interface {
	AuthorizedNamespaces(ctx context.Context, userID, clusterID, apiGroup, resource, verb string) (all bool, names map[string]struct{})
}

func requireK8sProxyPermission(engine *rbac.Engine, querier rbac.BindingQuerier, native nativeAuthorizer, namespaceScoped bool) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("kubernetes proxy authorization")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := k8sProxyPermission(r)
			user, ok := reqctx.AuthenticatedUser(r.Context())
			if !ok || user == nil {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication is required to access this resource")
				return
			}
			bindings, err := querier.GetUserBindings(r.Context(), user.ID)
			if err != nil {
				writeRouteAuthError(w, http.StatusInternalServerError, "internal_error", "Failed to retrieve user permissions")
				return
			}
			clusterID, projectID := permissionScopeIDs(r)
			// SECURITY: resolve the target namespace from the PARSED k8s request
			// path, never from a user-controlled ?namespace= query param. The
			// proxy forwards the path namespace (/api/v1/namespaces/<ns>/...), so
			// a namespace-scoped RBAC binding must be evaluated against that same
			// namespace. Trusting the query would let a caller authorized for one
			// namespace read another namespace's resources (incl. secrets) by
			// changing the query. parseK8sProxyObjectRef returns nil (→ empty
			// namespace) for cluster-scoped / discovery paths, which fails closed
			// against namespace-scoped bindings — matching the forwarded request.
			k8sPath, err := tunnel.CanonicalK8sProxyPath(r)
			if err != nil {
				writeRouteAuthError(w, http.StatusBadRequest, "invalid_k8s_path", "Kubernetes proxy path is not canonical")
				return
			}
			ref := parseK8sProxyObjectRef(k8sPath)
			namespace := ref["namespace"]
			if handleSelectedNamespaceAuthorization(w, r, next, engine, native, bindings, user.ID, clusterID, projectID, resource, verb, ref) {
				return
			}
			// F1 (M5): a mutating nodes/{name}/proxy request reaches the
			// kubelet's own HTTP surface, whose /run/ and /exec/ endpoints run
			// arbitrary commands in any container on the node. That is pod exec
			// by another name, so it must satisfy pods:exec IN ADDITION to
			// nodes:proxy. A single CheckPermission call cannot express AND, so
			// the conjunct lives here. Node paths are cluster-scoped (namespace
			// is empty), so this requires a cluster-wide pods:exec grant — and
			// deliberately not the native allow layer, which refuses to widen
			// exec at all.
			if resource == rbac.ResourceNodes && verb == rbac.VerbProxy && isMutatingK8sProxyMethod(r.Method) &&
				!engine.CheckPermission(bindings, rbac.ResourcePods, rbac.VerbExec, clusterID, projectID, namespace) {
				writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
				return
			}
			if !engine.CheckPermission(bindings, resource, verb, clusterID, projectID, namespace) {
				// Coarse RBAC denied. Consult the native per-CRD allow layer as
				// an ADDITIVE override: a native rule can grant this exact
				// (api_group, resource, verb) at this scope even when the coarse
				// custom_resources bucket doesn't. native is nil when the
				// feature is off, so default behavior is unchanged. The
				// evaluator itself refuses escalation groups + exec/logs, so a
				// native rule can never widen past those guards.
				if native == nil || !native.Allow(r.Context(), user.ID, clusterID.String(), namespace, ref["api_group"], ref["resource"], string(verb)) {
					// Namespace-scoped RBAC allow-through-and-filter gate. Both
					// the coarse and native checks denied because this scoped
					// user has no cluster-wide grant. If the flag is on and this
					// is a cluster-wide LIST or WATCH (GET, VerbList|VerbWatch,
					// namespace=="") for which the user holds the (resource,
					// list) permission in at least one namespace on this cluster,
					// admit the request and stash the authorized-namespace
					// allow-set. The tunnel proxy filters list bodies and watch
					// event frames down to those namespaces (F7-b). Namespaced
					// paths were already authorized above by CheckPermission;
					// mutations, named GETs, and users with no namespace access
					// keep failing closed here.
					//
					// Watch requests use VerbWatch for CheckPermission but we
					// compute the allow-set with VerbList: namespace bindings
					// commonly grant list+read without an explicit watch verb,
					// and list is the correct predicate for "which namespaces'
					// objects may appear on the stream".
					if namespaceScoped &&
						r.Method == http.MethodGet &&
						(verb == rbac.VerbList || verb == rbac.VerbWatch) &&
						namespace == "" {
						allowVerb := verb
						if verb == rbac.VerbWatch {
							allowVerb = rbac.VerbList
						}
						all, names := engine.AuthorizedNamespaces(bindings, resource, allowVerb, clusterID)
						// Fold native per-namespace list grants into the allow-set
						// too. Coarse project bindings already participate (via
						// AuthorizedNamespaces above), but a user whose ONLY grant
						// for this CRD is a native namespaced rule would otherwise
						// 403 on a cluster-wide LIST instead of getting a filtered
						// result — the native layer is only consulted as a single-
						// namespace Allow() above, never for list-filtering. The
						// optional nativeNamespaceLister capability (implemented by
						// *nativeRBACAuthorizer) enumerates the namespaces the
						// user's native rules grant for this (api_group, resource,
						// list) at this cluster; the same escalation-group / exec-
						// logs guards apply inside it, so it can never widen past
						// those. A nil authorizer or one that doesn't implement the
						// capability leaves behavior unchanged.
						if !all {
							if lister, ok := native.(nativeNamespaceLister); ok && lister != nil {
								nativeAll, nativeNames := lister.AuthorizedNamespaces(r.Context(), user.ID, clusterID.String(), ref["api_group"], ref["resource"], string(verb))
								if nativeAll {
									all = true
								} else if len(nativeNames) > 0 {
									if names == nil {
										names = make(map[string]struct{}, len(nativeNames))
									}
									for ns := range nativeNames {
										names[ns] = struct{}{}
									}
								}
							}
						}
						switch {
						case all:
							// Cluster-wide grant — shouldn't reach here since
							// CheckPermission would have passed. Serve unfiltered.
							next.ServeHTTP(w, r)
							return
						case len(names) > 0:
							ctx := tunnel.WithNamespaceFilter(r.Context(), names)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					}
					writeRouteAuthError(w, http.StatusForbidden, "permission_denied", "You do not have permission to perform this action")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireK8sProxyScope() func(http.Handler) http.Handler {
	writeClusters := requireScope(iauth.ScopeWriteClusters)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutatingK8sProxyMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}
			writeClusters(next).ServeHTTP(w, r)
		})
	}
}

func auditK8sProxyMutations(auditWriter any) func(http.Handler) http.Handler {
	return auditK8sProxyMutationsWithAction(auditWriter, "cluster.k8s_proxy", nil)
}

func auditK8sProxySecretReads(auditWriter any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, verb, ok := k8sProxySecretReadPermission(r); ok {
				clusterID := chi.URLParam(r, "cluster_id")
				k8sPath, err := tunnel.CanonicalK8sProxyPath(r)
				if err != nil {
					writeRouteAuthError(w, http.StatusBadRequest, "invalid_k8s_path", "Kubernetes proxy path is not canonical")
					return
				}
				detail := map[string]any{
					"method":   r.Method,
					"k8s_path": k8sPath,
					"verb":     string(verb),
				}
				if ref := parseK8sProxyObjectRef(k8sPath); len(ref) > 0 {
					for k, v := range ref {
						detail[k] = v
					}
				}
				handler.RecordAuditFromRequest(r, auditWriter, "cluster.secret.read", "cluster", clusterID, secretAuditResourceName(detail), detail)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func auditK8sProxyMutationsWithAction(auditWriter any, action string, extraDetail map[string]any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isMutatingK8sProxyMethod(r.Method) {
				next.ServeHTTP(w, r)
				return
			}

			clusterID := chi.URLParam(r, "cluster_id")
			k8sPath, err := tunnel.CanonicalK8sProxyPath(r)
			if err != nil {
				writeRouteAuthError(w, http.StatusBadRequest, "invalid_k8s_path", "Kubernetes proxy path is not canonical")
				return
			}
			detail := k8sProxyAuditDetail(r.Method, k8sPath, extraDetail)
			resourceName := k8sProxyAuditResourceName(detail)

			// A remote member-cluster mutation cannot share a PostgreSQL
			// transaction with the local audit row. Persist a content-free intent
			// synchronously before entering the tunnel instead. If PostgreSQL is
			// unavailable, fail closed before any remote effect is possible.
			intentDetail := cloneStringAnyMap(detail)
			intentDetail["phase"] = "intent"
			intentDetail["terminal_state"] = "unknown_until_outcome"
			intentDetail["reconcile_if_outcome_missing"] = true
			if err := recordMandatoryK8sProxyAudit(r.Context(), r, auditWriter, action+".intent", clusterID, resourceName, 0, 0, intentDetail); err != nil {
				slog.Default().Error("mandatory kubernetes proxy audit intent failed",
					"cluster_id", clusterID,
					"method", r.Method,
					"error", err,
				)
				writeRouteAuthError(w, http.StatusServiceUnavailable, "audit_unavailable", "Mandatory audit storage is unavailable; the Kubernetes mutation was not forwarded")
				return
			}

			started := time.Now()
			wrapped := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(wrapped, r)
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}

			// The response may already have been committed, so an outcome-write
			// failure cannot truthfully replace it with a 503 or roll back the
			// member-cluster effect. The durable intent remains the repair signal;
			// emit a safe operational error and preserve status/body compatibility.
			outcomeDetail := cloneStringAnyMap(detail)
			outcomeDetail["phase"] = "outcome"
			outcomeDetail["outcome"] = k8sProxyAuditOutcome(status)
			outcomeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Second)
			defer cancel()
			if err := recordMandatoryK8sProxyAudit(outcomeCtx, r, auditWriter, action+".outcome", clusterID, resourceName, status, time.Since(started).Milliseconds(), outcomeDetail); err != nil {
				slog.Default().Error("mandatory kubernetes proxy audit outcome failed; durable intent requires reconciliation",
					"cluster_id", clusterID,
					"method", r.Method,
					"status_code", status,
					"error", err,
				)
			}
		})
	}
}

func k8sProxyAuditDetail(method, k8sPath string, extra map[string]any) map[string]any {
	detail := map[string]any{"method": method}
	// Only parsed, allow-listed object coordinates are retained. In
	// particular, never copy the request body, raw YAML/JSON, headers, or raw
	// query string into compliance evidence.
	if ref := parseK8sProxyObjectRef(k8sPath); len(ref) > 0 {
		for _, key := range []string{"api_group", "api_version", "namespace", "resource", "name", "subresource"} {
			if value := strings.TrimSpace(ref[key]); value != "" {
				detail[key] = value
			}
		}
	}
	// Historical callers may add the safe logical proxy identifier. Do not
	// accept arbitrary keys here: this boundary must never become a path for a
	// manifest or credential-bearing request fragment to reach the audit log.
	if proxy, ok := extra["proxy"].(string); ok && strings.TrimSpace(proxy) != "" {
		detail["proxy"] = strings.TrimSpace(proxy)
	}
	return detail
}

func k8sProxyAuditResourceName(detail map[string]any) string {
	resource, _ := detail["resource"].(string)
	name, _ := detail["name"].(string)
	if resource == "" {
		return "kubernetes-api"
	}
	if name == "" {
		return resource
	}
	return resource + "/" + name
}

func cloneStringAnyMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+2)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func k8sProxyAuditOutcome(status int) string {
	switch {
	case status >= 200 && status < 400:
		return "completed"
	case status >= 400 && status < 500:
		return "rejected"
	default:
		return "failed"
	}
}

func recordMandatoryK8sProxyAudit(ctx context.Context, r *http.Request, writer any, action, clusterID, resourceName string, status int, durationMS int64, detail map[string]any) error {
	v1, ok := writer.(audit.Querier)
	if !ok || v1 == nil || r == nil {
		return audit.ErrMandatoryPersistenceUnavailable
	}

	var userID uuid.UUID
	authMethod := ""
	if user, ok := reqctx.AuthenticatedUser(r.Context()); ok && user != nil {
		userID, _ = uuid.Parse(user.ID)
		authMethod = user.AuthMethod
	}
	return audit.RecordMandatory(ctx, v1, audit.Event{
		Source:          "service",
		CorrelationID:   reqctx.CorrelationID(r.Context()),
		UserID:          audit.UserIDFromUUID(userID),
		ActorAuthMethod: authMethod,
		Action:          action,
		ResourceType:    "cluster",
		ResourceID:      clusterID,
		ResourceName:    resourceName,
		StatusCode:      int32(status),
		DurationMs:      durationMS,
		RequestID:       reqctx.RequestID(r.Context()),
		IPAddress:       reqctx.ClientIP(r),
		HTTPMethod:      r.Method,
		// Store the stable route template, not the raw path. Parsed object
		// coordinates above are sufficient for operators, while a raw
		// subresource/proxy suffix can contain arbitrary user-controlled text.
		Path:   "/api/v1/clusters/{cluster_id}/k8s/*",
		Detail: detail,
	})
}

func k8sProxyPermission(r *http.Request) (rbac.Resource, rbac.Verb) {
	if r == nil || r.URL == nil {
		return rbac.ResourceClusters, rbac.VerbRead
	}

	k8sPath, err := tunnel.CanonicalK8sProxyPath(r)
	if err != nil {
		return rbac.ResourceClusters, rbac.VerbProxy
	}
	ref := parseK8sProxyObjectRef(k8sPath)

	// F1 (M2): pod exec/attach/portforward is RCE-equivalent and MUST map to
	// the dedicated pods:exec verb. Detect the subresource from the parsed
	// object ref (robust to core-vs-apis prefix and trailing shape) rather
	// than a brittle hardcoded path matcher, so a mutating exec request can
	// never degrade to a generic pod write verb. The fallback to the raw URL
	// path keeps the gate working even when the chi wildcard param is unset
	// (e.g. direct handler calls in tests).
	if isHighRiskPodProxySubresourceRef(ref) || isHighRiskPodProxySubresource(r.URL.Path) {
		return rbac.ResourcePods, rbac.VerbExec
	}

	// F1 (M5): the apiserver's `proxy` subresource tunnels an arbitrary request
	// to the target's OWN endpoint, which is a different capability from
	// reading or writing the target object: nodes/{name}/proxy reaches the
	// kubelet (including /run/<ns>/<pod>/<container>, i.e. command execution in
	// any container on the node), pods/{name}/proxy and services/{name}/proxy
	// reach the workload's port directly. Without this branch those requests
	// degrade to the target's generic read/update verb and the dedicated
	// `proxy` verb the role catalog already grants gates nothing.
	// parseK8sProxyObjectRef drops every segment after the subresource, so
	// /nodes/n1/proxy and /nodes/n1/proxy/run/... both land here. Decide it
	// BEFORE the pods/log branch and the k8sProxyResourcePolicy fallthrough.
	// The extra pods:exec conjunct for node proxy lives in
	// requireK8sProxyPermission — one permission pair cannot express AND.
	if strings.ToLower(strings.TrimSpace(ref["subresource"])) == "proxy" {
		if resource, ok := knownK8sProxyResource(ref["resource"]); ok {
			return resource, rbac.VerbProxy
		}
		if resource, ok := k8sProxyResourcePolicy(ref); ok {
			return resource, rbac.VerbProxy
		}
		// Unknown core-group resource with a proxy subresource: fail closed on
		// clusters:proxy (which no template grants) rather than degrading to
		// the generic clusters read/update verb.
		return rbac.ResourceClusters, rbac.VerbProxy
	}

	verb := k8sProxyVerb(r, ref)
	if ref["resource"] == "pods" && ref["subresource"] == "log" && !isMutatingK8sProxyMethod(r.Method) {
		return rbac.ResourcePods, rbac.VerbLogs
	}
	// F2 (M3): custom resources, unknown apigroups, and non-resource discovery
	// URLs are governed by an explicit, conservative policy rather than
	// collapsing to the generic clusters verb (which let per-resource RBAC
	// silently not apply to CRDs). Decide this BEFORE namedResourcePermission's
	// generic fallthrough. See k8sProxyResourcePolicy.
	if resource, ok := k8sProxyResourcePolicy(ref); ok {
		// F2 (M4): writing to the privilege-escalation API groups (RBAC,
		// admission webhooks, aggregated APIServices, CRD definitions) via the
		// proxy is cluster-admin-equivalent — e.g. POST a ClusterRoleBinding to
		// bind yourself to cluster-admin. The generic custom_resources grant
		// must NOT authorise that, so gate mutating verbs on these groups behind
		// the dedicated rbac permission (held only by owner/admin templates).
		// Reads/lists/watches stay on custom_resources so the explorer is
		// unchanged.
		if isMutatingK8sProxyMethod(r.Method) && isPrivilegeEscalationAPIGroup(ref["api_group"]) {
			return rbac.ResourceRBAC, verb
		}
		return resource, verb
	}
	resource, verb := namedResourcePermission(ref["resource"], verb)
	return resource, verb
}

// isPrivilegeEscalationAPIGroup reports whether a Kubernetes API group lets a
// writer escalate to cluster-admin: RBAC (ClusterRole/Binding), admission
// webhooks (intercept/mutate any request), aggregated APIServices, and CRD
// definitions (own the shape of arbitrary cluster resources).
func isPrivilegeEscalationAPIGroup(group string) bool {
	// Keep in lockstep with rbac.isPrivilegeEscalationGroup (native rules):
	// CSR minting and TokenReview/TokenRequest are cluster-admin equivalent
	// and must not fall through to custom_resources write (SEC-04).
	switch strings.ToLower(strings.TrimSpace(group)) {
	case "rbac.authorization.k8s.io",
		"admissionregistration.k8s.io",
		"apiregistration.k8s.io",
		"apiextensions.k8s.io",
		"certificates.k8s.io",
		"authentication.k8s.io":
		return true
	}
	return false
}

// k8sProxyResourcePolicy implements the F2 (M3) policy for shapes that the
// typed namedResourcePermission table does NOT recognise, so they no longer
// silently collapse to the generic ResourceClusters permission:
//
//   - Custom resources served under apis/<group>/<version>/... whose
//     <group> is not a core/built-in Kubernetes group map to the dedicated
//     ResourceCustomResources permission, so per-resource RBAC (e.g.
//     custom_resources:read / :update) governs CRD access instead of the
//     blanket clusters verb.
//   - Non-resource discovery URLs (/version, /healthz, /api, /apis and their
//     sub-paths) carry no parseable object ref; they are read-only and map to
//     ResourceClusters/VerbRead via the caller. They are intentionally NOT
//     claimed here (ok=false) so the existing read classification stands.
//
// The policy is conservative: it never broadens access. A request that maps
// to ResourceCustomResources requires that permission explicitly; absent a
// matching binding the request is denied (whereas previously a clusters
// binding would have allowed it).
func k8sProxyResourcePolicy(ref map[string]string) (rbac.Resource, bool) {
	if len(ref) == 0 {
		// Non-resource / discovery URL (parseK8sProxyObjectRef returned nil):
		// leave it to the generic read-only clusters classification.
		return "", false
	}
	resourceType := strings.ToLower(strings.TrimSpace(ref["resource"]))
	if resourceType == "" {
		return "", false
	}
	if _, known := knownK8sProxyResource(resourceType); known {
		// A built-in/typed resource — handled by namedResourcePermission.
		return "", false
	}
	// Custom resource under apis/<group>/<version>/...: map to the dedicated
	// custom-resources permission. Core-group (api/v1) unknown resources are
	// rare/internal and stay on the generic clusters classification to avoid
	// over-restricting discovery-ish core endpoints.
	if strings.TrimSpace(ref["api_group"]) != "" {
		return rbac.ResourceCustomResources, true
	}
	return "", false
}

func k8sProxyVerb(r *http.Request, ref map[string]string) rbac.Verb {
	if !isMutatingK8sProxyMethod(r.Method) {
		if isK8sProxyWatchRequest(r) || ref["watch"] == "true" {
			return rbac.VerbWatch
		}
		if ref["name"] == "" {
			return rbac.VerbList
		}
		return rbac.VerbRead
	}
	if strings.EqualFold(r.URL.Query().Get("force"), "true") && r.URL.Query().Get("dryRun") == "" {
		return rbac.VerbManage
	}

	switch r.Method {
	case http.MethodPost:
		// F3 (L1): POST pods/{name}/eviction deletes the pod (the Eviction
		// subresource is a delete operation), so classify it as VerbDelete
		// for honest RBAC + audit rather than the generic POST-to-named-
		// subresource update verb.
		if ref["resource"] == "pods" && ref["subresource"] == "eviction" {
			return rbac.VerbDelete
		}
		if ref["name"] == "" {
			return rbac.VerbCreate
		}
		return rbac.VerbUpdate
	case http.MethodDelete:
		return rbac.VerbDelete
	default:
		return rbac.VerbUpdate
	}
}

func k8sProxySecretReadPermission(r *http.Request) (rbac.Resource, rbac.Verb, bool) {
	if r == nil || isMutatingK8sProxyMethod(r.Method) {
		return "", "", false
	}
	k8sPath, err := tunnel.CanonicalK8sProxyPath(r)
	if err != nil {
		return "", "", false
	}
	ref := parseK8sProxyObjectRef(k8sPath)
	if ref["resource"] != "secrets" {
		return "", "", false
	}
	verb := rbac.VerbRead
	if isK8sProxyWatchRequest(r) {
		verb = rbac.VerbWatch
	} else if ref["name"] == "" {
		verb = rbac.VerbList
	}
	return rbac.ResourceSecrets, verb, true
}

func secretAuditResourceName(detail map[string]any) string {
	namespace, _ := detail["namespace"].(string)
	name, _ := detail["name"].(string)
	switch {
	case namespace != "" && name != "":
		return namespace + "/" + name
	case name != "":
		return name
	case namespace != "":
		return namespace + "/secrets"
	default:
		return "secrets"
	}
}

func parseK8sProxyObjectRef(k8sPath string) map[string]string {
	parts := strings.Split(strings.Trim(k8sPath, "/"), "/")
	if len(parts) < 3 {
		return nil
	}
	out := map[string]string{}
	idx := 0
	switch {
	case len(parts) >= 3 && parts[0] == "api":
		out["api_version"] = parts[1]
		idx = 2
	case len(parts) >= 4 && parts[0] == "apis":
		out["api_group"] = parts[1]
		out["api_version"] = parts[2]
		idx = 3
	default:
		return nil
	}
	if idx < len(parts) && parts[idx] == "watch" {
		out["watch"] = "true"
		idx++
	}
	if idx < len(parts) && parts[idx] == "namespaces" && idx+1 < len(parts) {
		out["namespace"] = parts[idx+1]
		idx += 2
	}
	if idx < len(parts) {
		out["resource"] = parts[idx]
	}
	if idx+1 < len(parts) {
		out["name"] = parts[idx+1]
	}
	if idx+2 < len(parts) {
		out["subresource"] = parts[idx+2]
	}
	return out
}

func requireServiceProxyPermission(engine *rbac.Engine, querier rbac.BindingQuerier) func(http.Handler) http.Handler {
	return requirePermission(engine, querier, rbac.ResourceServices, rbac.VerbProxy)
}

// requireGenericResourceListPermission gates the generic list route.
// ResourceHandler.ListGenericResources builds its upstream path from the same
// ?namespace=, so the query-scoped gate is the honest one here.
func requireGenericResourceListPermission(engine *rbac.Engine, querier rbac.BindingQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbList)
			requireQueryNamespacePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

// requireNamedResourcePermission gates the typed resource routes on the
// resource implied by the {resource_type}/{type} route param. The namespace
// comes from the route only: the named GET/PUT/DELETE forms carry {namespace},
// and the collection forms are cluster-wide unless mounted through
// requireNamedResourceListPermission / requireNamedResourceCreatePermission.
func requireNamedResourcePermission(engine *rbac.Engine, querier rbac.BindingQuerier, routeParam string, requestedVerb rbac.Verb) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, routeParam), requestedVerb)
			requirePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

// requireNamedResourceListPermission gates
// GET /clusters/{cluster_id}/resources/{resource_type}/, whose handler
// (ResourceHandler.ListNamedResources) builds /api/v1/namespaces/<ns>/<type>
// from the same ?namespace=. Gate namespace == handler namespace.
func requireNamedResourceListPermission(engine *rbac.Engine, querier rbac.BindingQuerier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbList)
			requireQueryNamespacePermission(engine, querier, resource, verb)(next).ServeHTTP(w, r)
		})
	}
}

// createBodyMaxBytes caps how much of a create body the namespace gate below
// will buffer. Namespaced manifests posted through this route are small; a
// larger body is refused outright rather than authorized on a partial parse.
const createBodyMaxBytes = 1 << 20

// requireNamedResourceCreatePermission gates
// POST /clusters/{cluster_id}/resources/{resource_type}/ on the namespace
// inside the REQUEST BODY.
//
// SECURITY: ResourceHandler.CreateNamedResource derives its target namespace
// from metadata.namespace (resourceNamespace(body)), never from the URL. Gating
// that route on a URL-supplied namespace authorized one namespace while the
// handler wrote to another — a project member holding services/ingresses in
// their own namespace could create an Ingress (hostname + TLS hijack) or a
// NetworkPolicy (tenant DoS) anywhere on the cluster. So the gate parses the
// same field the handler will use. A body that names no namespace is refused:
// the upstream path would then be cluster-wide/implicit, which no
// namespace-narrowed grant covers.
func requireNamedResourceCreatePermission(engine *rbac.Engine, querier rbac.BindingQuerier) func(http.Handler) http.Handler {
	if engine == nil || querier == nil {
		return unavailableSecurityDependency("RBAC authorization")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(io.LimitReader(r.Body, createBodyMaxBytes+1))
			if err != nil || len(body) > createBodyMaxBytes {
				writeRouteAuthError(w, http.StatusRequestEntityTooLarge, "invalid_body", "Request body is too large")
				return
			}
			// Hand the handler back an identical, re-readable body.
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))

			namespace := k8sManifestNamespace(body)
			if namespace == "" {
				writeRouteAuthError(w, http.StatusBadRequest, "invalid_body", "metadata.namespace is required")
				return
			}
			resource, verb := namedResourcePermission(chi.URLParam(r, "resource_type"), rbac.VerbCreate)
			appmiddleware.RequirePermissionForNamespace(engine, querier, resource, verb,
				func(*http.Request) string { return namespace })(next).ServeHTTP(w, r)
		})
	}
}

// k8sManifestNamespace pulls metadata.namespace out of a JSON manifest. It
// mirrors handler.resourceNamespace; a body that does not parse yields "" and
// the caller refuses the request.
func k8sManifestNamespace(body []byte) string {
	var payload struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Metadata.Namespace)
}

func namedResourcePermission(resourceType string, requestedVerb rbac.Verb) (rbac.Resource, rbac.Verb) {
	if resource, ok := knownK8sProxyResource(resourceType); ok {
		return resource, requestedVerb
	}
	if requestedVerb == rbac.VerbRead || requestedVerb == rbac.VerbList || requestedVerb == rbac.VerbWatch {
		return rbac.ResourceClusters, requestedVerb
	}
	return rbac.ResourceClusters, rbac.VerbUpdate
}

// knownK8sProxyResource maps a Kubernetes resource type (singular or plural,
// case-insensitive) to its astronomer RBAC resource. The second return value
// reports whether the type is a recognised built-in; callers use it to decide
// whether the F2 custom-resource policy should apply instead of the generic
// clusters fallthrough.
func knownK8sProxyResource(resourceType string) (rbac.Resource, bool) {
	return rbac.KubernetesResource(resourceType)
}

func auditGenericSecretList(auditWriter any) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.EqualFold(chi.URLParam(r, "resource_type"), "secrets") {
				clusterID := chi.URLParam(r, "cluster_id")
				namespace := strings.TrimSpace(r.URL.Query().Get("namespace"))
				detail := map[string]any{
					"method":        r.Method,
					"resource_type": "secrets",
					"verb":          string(rbac.VerbList),
					"scope":         "generic_resource_list",
				}
				resourceName := "secrets"
				if namespace != "" {
					detail["namespace"] = namespace
					resourceName = namespace + "/secrets"
				}
				handler.RecordAuditFromRequest(r, auditWriter, "cluster.secret.read", "cluster", clusterID, resourceName, detail)
			}
			next.ServeHTTP(w, r)
		})
	}
}

func requireServiceProxyScope() func(http.Handler) http.Handler {
	return requireK8sProxyScope()
}

func requireStreamTicketOrAuth(jwt *iauth.JWTManager, queries iauth.TokenUserQuerier, tickets *iauth.StreamTicketStore, kind string, clusterParam string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var clusterID uuid.UUID
			if strings.TrimSpace(clusterParam) != "" {
				var err error
				clusterID, err = uuid.Parse(chi.URLParam(r, clusterParam))
				if err != nil {
					writeRouteAuthError(w, http.StatusBadRequest, "invalid_id", "Invalid cluster ID")
					return
				}
			}
			userID, ok := iauth.AuthorizeStreamRequestWithTickets(r, queries, jwt, tickets, kind, clusterID)
			if !ok {
				writeRouteAuthError(w, http.StatusUnauthorized, "authentication_required", "Authentication required")
				return
			}
			if userID != uuid.Nil {
				r = r.WithContext(reqctx.WithUser(r.Context(), &reqctx.User{
					ID:         userID.String(),
					AuthMethod: "stream_ticket",
				}))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeRouteAuthError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

func isMutatingK8sProxyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func isK8sProxyWatchRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}
	// Match the apiserver's own ?watch parsing (strconv.ParseBool: TRUE/t/T/1…),
	// not just "true"/"1" — otherwise ?watch=TRUE is misclassified as a unary
	// LIST, admitted by the namespace-scoped list gate, and forwarded as a watch
	// (a scoped-RBAC bypass). See isWatchRequest in the tunnel package.
	if v := r.URL.Query().Get("watch"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil && b {
			return true
		}
	}
	if strings.Contains(r.Header.Get("Accept"), "stream=watch") {
		return true
	}
	return strings.Contains(r.URL.Path, "/watch/")
}

// isHighRiskPodProxySubresourceRef reports whether the parsed object ref is a
// pod exec/attach/portforward subresource. Unlike the legacy path matcher
// below, it relies on the structured subresource field, so it is robust to:
//   - core (api/v1) vs apis prefix shape (parseK8sProxyObjectRef normalises
//     both into resource/name/subresource),
//   - trailing path segments after the subresource (proxied apiserver
//     subresource URLs do not carry further segments, but a trailing slash or
//     query no longer defeats detection),
//   - singular vs plural resource spelling.
//
// This is the F1 (M2) fix: detection must never miss, because a missed
// exec/attach/portforward would degrade to a generic pod *write* verb and
// bypass the dedicated pods:exec gate.
func isHighRiskPodProxySubresourceRef(ref map[string]string) bool {
	if len(ref) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ref["resource"])) {
	case "pods", "pod":
	default:
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ref["subresource"])) {
	case "exec", "attach", "portforward":
		return true
	default:
		return false
	}
}

func isHighRiskPodProxySubresource(path string) bool {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	for i := 0; i+6 < len(segments); i++ {
		if segments[i] != "api" || segments[i+1] != "v1" || segments[i+2] != "namespaces" || segments[i+4] != "pods" {
			continue
		}
		switch segments[i+6] {
		case "exec", "attach", "portforward":
			return i+7 == len(segments)
		}
	}
	return false
}

func registerProtectedRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	// Domain register funcs are invoked in the SAME source order the
	// route blocks previously appeared inline, so chi mount/registration
	// order (and therefore the route surface) is unchanged. The Migration-044
	// scope closures (writeClusters/writeProjects/writeRBAC/mutationWriteScope)
	// are pure stateless constructors recreated locally inside each domain
	// func that needs them.
	registerClusterRoutes(r, deps)
	registerClusterAddonRoutes(r, deps)
	registerProjectRoutes(r, deps)
	registerDeliveryRoutes(r, deps)
	registerDashboardRoutes(r, deps)
	registerToolsControlPlaneRoutes(r, deps)
	registerRBACAuditAgentRoutes(r, deps, rateLimit)
	registerAlertInhibitionRoutes(r, deps)
	registerGatekeeperConstraintRoutes(r, deps)
	registerMonitoringRoutes(r, deps)
	registerResourcesWorkloadsRoutes(r, deps)
	registerSecurityRoutes(r, cfg, deps, rateLimit)
	registerDexRoutes(r, deps)
	registerCharlieRoutes(r, deps, rateLimit)
}

// keyStatusHandler returns the number of loaded encryption + JWT signing
// keys, plus any credential still set to a published development sentinel
// (dev-keys-default-and-silent) so the UI can show a red banner. The runbook
// (docs/secret-rotation-runbook.md) tells operators to poll this during a
// rotation to confirm the new key is in fact loaded and that the old key has
// been dropped at the end of the procedure.
//
// Auth: superuser only — the count itself is harmless, but the diagnostic
// is intended for the operator running the rotation, not the general user
// population.
func keyStatusHandler(cfg *config.Config, deps RouterDependencies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := handler.RequireSuperuser(w, r, deps.CoreAuth.AuthQueries, handler.SuperuserGateConfig{
			StoreUnavailableStatus:  http.StatusInternalServerError,
			StoreUnavailableCode:    "internal_error",
			StoreUnavailableMessage: "User store not configured",
			ForbiddenMessage:        "Key status requires superuser privileges",
		}); !ok {
			return
		}

		encKeys := 0
		encKeyInventory := []iauth.EncryptionKeyInfo{}
		if deps.CoreAuth.Encryptor != nil {
			encKeys = deps.CoreAuth.Encryptor.KeyCount()
			encKeyInventory = deps.CoreAuth.Encryptor.KeyInventory()
		}
		jwtKeys := 0
		if deps.CoreAuth.JWT != nil {
			jwtKeys = deps.CoreAuth.JWT.KeyCount()
		}
		insecureDevKeys := config.DevSentinelsInUse(cfg)
		if insecureDevKeys == nil {
			insecureDevKeys = []string{}
		}

		// Read-only superuser endpoint that exposes the live key-rotation
		// state — leave an explicit audit trail. The mutating-HTTP audit
		// middleware skips GET, so this trail wouldn't otherwise exist.
		handler.RecordAuditFromRequest(r, deps.CoreAuth.AuthQueries, "admin.key_status.viewed",
			"platform", "", "key-status", map[string]any{
				"encryption_keys":   encKeys,
				"jwt_keys":          jwtKeys,
				"insecure_dev_keys": insecureDevKeys,
			})

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"encryption_keys":          encKeys,
			"encryption_key_inventory": encKeyInventory,
			"jwt_keys":                 jwtKeys,
			"insecure_dev_keys":        insecureDevKeys,
			"as_of":                    time.Now().UTC().Format(time.RFC3339),
		})
	}
}
