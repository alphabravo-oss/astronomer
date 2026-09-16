package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// registerLongLivedRoutes owns streams, websocket upgrades, raw Kubernetes
// proxying, and sibling-server tunnel forwarding. These routes deliberately
// sit outside the bounded REST timeout group.
func registerLongLivedRoutes(
	r chi.Router,
	deps RouterDependencies,
	rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler,
) {
	if deps.StreamingInternal.Hub != nil {
		r.Get("/api/v1/ws/agent/tunnel/{cluster_id}/", deps.StreamingInternal.Hub.HandleWebSocket)
	}
	if deps.StreamingInternal.EventStream != nil {
		// SSE event stream — keeps a long-lived response open so register
		// outside the /api/v1 Timeout middleware group.
		r.Get("/api/v1/events/stream/", deps.StreamingInternal.EventStream.Stream)
	}
	if deps.ClusterResources.Workloads != nil {
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
			requireStreamTicketOrAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries, deps.StreamingInternal.StreamTicketStore, iauth.StreamKindLogs, "cluster_id"),
			requireListPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourcePods, rbac.VerbRead, deps.ClusterResources.NamespaceScopedRBAC),
		).Get("/api/v1/clusters/{cluster_id}/pods/watch/", deps.ClusterResources.Workloads.WatchPods)
	}
	if deps.StreamingInternal.Proxy != nil {
		// k8s passthrough is the most common loop-DoS target — any
		// authenticated user can fire arbitrary list calls. Token bucket
		// is sized so a normal UI burst (clicking through tabs) passes;
		// a runaway loop trips within ~20 requests.
		r.With(
			canonicalK8sProxyPath,
			rateLimit(appmiddleware.ClassK8sProxy),
			requireAuth(deps.CoreAuth.JWT, deps.CoreAuth.AuthQueries),
			requireK8sProxyScope(),
			requireK8sProxyPermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, deps.ClusterResources.NativeAuthz, deps.ClusterResources.NamespaceScopedRBAC),
			auditK8sProxySecretReads(deps.CoreAuth.AuditWriter),
			auditK8sProxyMutations(deps.CoreAuth.AuditWriter),
		).
			HandleFunc("/api/v1/clusters/{cluster_id}/k8s/*", deps.StreamingInternal.Proxy.HandleK8sProxy)
	}
	if deps.StreamingInternal.InternalK8s != nil {
		// Cross-pod fallback for the server-internal K8sRequester.
		// PSK-protected so non-sibling callers 403 — mounted outside the
		// JWT auth chain because sibling pods don't carry user JWTs.
		r.Post("/internal/tunnel/k8s/{cluster_id}", deps.StreamingInternal.InternalK8s.Handle)
		r.Get("/internal/tunnel/k8s/{cluster_id}/capabilities/{capability}", deps.StreamingInternal.InternalK8s.HandleCapability)
	}
	if deps.StreamingInternal.InternalHelm != nil {
		// Cross-pod fallback for the server-internal HelmRequester.
		// Same PSK + outside-JWT-chain contract as the K8s counterpart.
		// Required for catalog install/upgrade/uninstall to work when
		// the request lands on a replica that doesn't own the WS.
		r.Post("/internal/tunnel/helm/{cluster_id}", deps.StreamingInternal.InternalHelm.Handle)
	}
	if deps.StreamingInternal.Exec != nil {
		// Exec session opens hold a goroutine + WS connection until the
		// shell exits. Limit new-session opens so a misbehaving caller
		// can't spawn arbitrary parallel terminals.
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/", deps.StreamingInternal.Exec.HandleExec)
	}
	if deps.StreamingInternal.Logs != nil {
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/logs/{cluster_id}/{namespace}/{pod}/{container}/", deps.StreamingInternal.Logs.HandleLogs)
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
	if deps.StreamingInternal.KubectlShell != nil {
		r.With(rateLimit(appmiddleware.ClassExecLogs)).
			Get("/api/v1/ws/clusters/{cluster_id}/shell/sessions/{id}/", deps.StreamingInternal.KubectlShell.HandleWS)
	}

}
