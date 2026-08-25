package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// registerLongLivedRoutes owns streams, websocket upgrades, raw Kubernetes
// proxying, and sibling-server tunnel forwarding. These routes deliberately
// sit outside the bounded REST timeout group.
func registerLongLivedRoutes(
	r chi.Router,
	cfg *config.Config,
	deps RouterDependencies,
	rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler,
) {
	if deps.Hub != nil {
		r.Get("/api/v1/ws/agent/tunnel/{cluster_id}/", deps.Hub.HandleWebSocket)
	}
	if deps.EventStream != nil {
		// SSE event stream — keeps a long-lived response open so register
		// outside the /api/v1 Timeout middleware group.
		r.Get("/api/v1/events/stream/", deps.EventStream.Stream)
	}
	if deps.Workloads != nil {
		// Live pod watch SSE stream — streams ADDED/MODIFIED/DELETED events
		// through the agent tunnel instead of the UI polling the pod list.
		// Long-lived, so register outside the /api/v1 Timeout middleware group
		// (same contract as the event/registration SSE streams above). Auth is
		// a one-use stream ticket (scoped to the cluster) or a normal token,
		// plus the pods:read RBAC gate.
		//
		// F7: use the namespace-scoped LIST gate instead of the cluster-wide
		// requirePermission so a namespace-confined tenant can open the watch for
		// a namespace they own (or a bare watch when they hold pods:read in ≥1
		// namespace). With namespace_scoped_rbac_enabled OFF this is byte-identical
		// to requirePermission(pods, read) — no behavior change. The admission is
		// paired with per-frame filtering in WatchPods (same gate+filter invariant
		// as ListPods) so an admitted scoped caller never receives frames for a
		// namespace outside their allow-set; a cluster-wide/superuser caller passes
		// the plain check and watches everything unfiltered.
		r.With(
			requireStreamTicketOrAuth(deps.JWT, deps.AuthQueries, deps.StreamTicketStore, iauth.StreamKindLogs, "cluster_id"),
			requireListPermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourcePods, rbac.VerbRead, deps.NamespaceScopedRBAC),
		).Get("/api/v1/clusters/{cluster_id}/pods/watch/", deps.Workloads.WatchPods)
	}
	if deps.RemoteServer != nil {
		// remotedialer hijacks the connection for a WS upgrade, so this MUST
		// be registered outside the /api/v1 group that applies a Timeout
		// middleware (the same reason the legacy ws/agent/tunnel route lives
		// out here).
		// A4 / M5: pre-upgrade per-IP failure-limiter gate (shared with the hub
		// connect path) so an over-threshold IP gets a clean 429 before
		// remotedialer hijacks the connection. Middleware on an existing route
		// does NOT change the route pattern.
		r.With(deps.RemoteServer.RateLimitMiddleware()).
			HandleFunc("/api/v1/connect/{cluster_id}/", deps.RemoteServer.ServeHTTP)
		// Demonstration endpoint — proves the new tunnel works end-to-end by
		// listing pods through a stock client-go clientset whose transport is
		// dialed through remotedialer. Real handlers will follow once the
		// migration is verified. Keep it out of production so demo-only
		// cluster data surfaces cannot linger as a supported API.
		if !isProductionConfig(cfg) {
			r.With(
				requireAuth(deps.JWT, deps.AuthQueries),
				requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead),
			).Get("/api/v1/clusters/{id}/v2/pods/", remoteV2PodsHandler(cfg, deps))
		}
	}
	if deps.Proxy != nil {
		// k8s passthrough is the most common loop-DoS target — any
		// authenticated user can fire arbitrary list calls. Token bucket
		// is sized so a normal UI burst (clicking through tabs) passes;
		// a runaway loop trips within ~20 requests.
		r.With(
			rateLimit(appmiddleware.ClassK8sProxy),
			requireAuth(deps.JWT, deps.AuthQueries),
			requireK8sProxyScope(),
			requireK8sProxyPermission(deps.RBACEngine, deps.RBACQueries, deps.NativeAuthz, deps.NamespaceScopedRBAC),
			auditK8sProxySecretReads(deps.AuditWriter),
			auditK8sProxyMutations(deps.AuditWriter),
		).
			HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", deps.Proxy.HandleK8sProxy)
	}
	if deps.InternalK8s != nil {
		// Cross-pod fallback for the server-internal K8sRequester.
		// PSK-protected so non-sibling callers 403 — mounted outside the
		// JWT auth chain because sibling pods don't carry user JWTs.
		r.Post("/internal/tunnel/k8s/{cluster_id}", deps.InternalK8s.Handle)
	}
	if deps.InternalHelm != nil {
		// Cross-pod fallback for the server-internal HelmRequester.
		// Same PSK + outside-JWT-chain contract as the K8s counterpart.
		// Required for catalog install/upgrade/uninstall to work when
		// the request lands on a replica that doesn't own the WS.
		r.Post("/internal/tunnel/helm/{cluster_id}", deps.InternalHelm.Handle)
	}
	if deps.Exec != nil {
		// Exec session opens hold a goroutine + WS connection until the
		// shell exits. Limit new-session opens so a misbehaving caller
		// can't spawn arbitrary parallel terminals.
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/", deps.Exec.HandleExec)
	}
	if deps.Logs != nil {
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/", deps.Logs.HandleLogs)
	}

	// Kubectl shell WS handshake — session-aware front door that
	// validates the {id} row belongs to the caller, then upgrades the
	// WebSocket on this route and proxies frames inline onto the
	// cluster agent's exec relay (the same code path the
	// /api/v1/ws/exec/ route runs post-upgrade).
	//
	// We used to 307-redirect at /api/v1/ws/exec/{cluster_id}/{ns}/
	// {pod}/{container}/. Chromium followed the redirect transparently
	// before the Upgrade handshake but Firefox does not, and several
	// corporate proxies strip Upgrade headers across redirects, so the
	// shell route now terminates the WS here.
	if deps.KubectlShell != nil {
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/", deps.KubectlShell.HandleWS)
	}

}
