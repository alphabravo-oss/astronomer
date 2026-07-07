package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/hibiken/asynq"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerSecurityRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	if deps.Security != nil {
		// Per-route authorization for the mutating security surface. Previously
		// these routes sat behind ONLY the feature-flag gate, so any
		// authenticated principal (including a zero-grant viewer) could mutate
		// templates/policies/scans. ApplyPolicy + CreateScan push config to a
		// managed cluster through the tunnel, so they additionally carry the
		// write-clusters scope backstop, mirroring the apiserver-audit ingest
		// composition below.
		secCreate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSecurity, rbac.VerbCreate)
		secUpdate := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSecurity, rbac.VerbUpdate)
		secDelete := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSecurity, rbac.VerbDelete)
		secWriteClusters := requireScope(iauth.ScopeWriteClusters)
		r.With(featureGate("feature.security", deps.SettingsCache)).Route("/security", func(r chi.Router) {
			r.Get("/controller/status/", deps.Security.ControllerStatus)
			r.Get("/templates/", deps.Security.ListTemplates)
			r.With(secCreate).Post("/templates/", deps.Security.CreateTemplate)
			r.Get("/templates/{id}/", deps.Security.GetTemplate)
			r.With(secUpdate).Put("/templates/{id}/", deps.Security.UpdateTemplate)
			r.With(secDelete).Delete("/templates/{id}/", deps.Security.DeleteTemplate)
			r.Get("/policies/", deps.Security.ListPolicies)
			r.With(secCreate).Post("/policies/", deps.Security.CreatePolicy)
			r.With(secWriteClusters, secUpdate).Post("/policies/{id}/apply/", deps.Security.ApplyPolicy)
			r.With(secDelete).Delete("/policies/{id}/", deps.Security.DeletePolicy)
			r.Get("/scans/", deps.Security.ListAllScans)
			r.With(secWriteClusters, secCreate).Post("/scans/", deps.Security.CreateScan)
		})
		r.Get("/clusters/{cluster_id}/security/policy/", deps.Security.GetPolicy)
		r.Get("/clusters/{cluster_id}/security/scans/", deps.Security.ListScans)
		r.Get("/clusters/{cluster_id}/security/scans/{id}/", deps.Security.GetScan)
	}

	// --- P1 item 7: kube-apiserver audit-event collection -----------------
	// The per-cluster agent POSTs batched audit.k8s.io events to the ingest
	// endpoint (a write, gated on cluster:update); operators read them back
	// via the list endpoint (cluster:read). Cluster-scoped, idempotent on
	// (cluster_id, auditID) so a re-delivered batch is a no-op.
	if deps.ApiserverAudit != nil {
		auditIngest := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)
		auditRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		r.With(requireScope(iauth.ScopeWriteClusters), auditIngest).Post("/clusters/{cluster_id}/apiserver-audit/", deps.ApiserverAudit.Ingest)
		r.With(auditRead).Get("/clusters/{cluster_id}/apiserver-audit/", deps.ApiserverAudit.List)
	}

	// --- Sprint 062: image vulnerability scanning -------------------------
	// Cluster-scoped routes gate on cluster:read; the fleet rollup pair
	// gates on security:read. Cluster routes live OUTSIDE the `/security`
	// mount so the existing CIS-benchmark routes stay untouched. The fleet
	// routes are nested under `/security/vulnerabilities/` so they pair
	// naturally with the CIS surface in the dashboard.
	if deps.ImageVulns != nil {
		ivClusterRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		ivSecurityRead := requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSecurity, rbac.VerbRead)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/summary/", deps.ImageVulns.ClusterSummary)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/images/", deps.ImageVulns.ClusterTopImages)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/reports/{id}/", deps.ImageVulns.ClusterReportDetail)
		r.With(ivClusterRead).Post("/clusters/{cluster_id}/vulnerabilities/rescan/", deps.ImageVulns.ClusterRescan)
		// Sprint 081: scan history sparkline + latest-vs-prior diff +
		// CSV download. All three are read-only and gated by the same
		// cluster:read RBAC the rest of the vuln surface uses.
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/history/", deps.ImageVulns.ClusterHistory)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/diff/", deps.ImageVulns.ClusterDiff)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/export.csv", deps.ImageVulns.ClusterExportCSV)
		// Per-image snapshot timeline — powers the drawer "scan history"
		// panel so operators can see how a single workload's CVE counts
		// have moved over time, not just the cluster-wide aggregate.
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/reports/{report_id}/history/", deps.ImageVulns.ReportHistory)
		// Live scan-in-progress indicator: in-flight trivy Jobs +
		// operator readiness via the k8s passthrough. The UI polls
		// every 3s when scans are running, every 30s otherwise.
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/progress/", deps.ImageVulns.ClusterProgress)
		r.With(ivSecurityRead).Get("/security/vulnerabilities/summary/", deps.ImageVulns.FleetSummary)
		r.With(ivSecurityRead).Get("/security/vulnerabilities/top-clusters/", deps.ImageVulns.FleetTopClusters)
	}

	// --- Phase A3: cross-cluster resource search ---------------------------
	// Single endpoint that fans a list query out across every active cluster
	// in parallel. The handler enforces a per-cluster timeout and concurrency
	// cap so a single slow cluster cannot block the whole response. The
	// response includes per-cluster errors + counts so the UI can surface
	// partial failures gracefully.
	if deps.ResourcesSearch != nil {
		// Cross-cluster fan-out — each call hits every connected tunnel.
		// Rate limit per-user so a runaway typeahead can't DoS the fleet.
		r.With(rateLimit(appmiddleware.ClassSearch)).
			Get("/resources/search/", deps.ResourcesSearch.Search)
	}

	// --- Phase B5: CIS scans via cis-operator ------------------------------
	// Mounts the CIS-specific routes layered over the existing security
	// handler. These routes live below the same `/security` prefix as the
	// pre-existing scan endpoints, but are registered here (instead of in
	// the main `if deps.Security != nil` block above) so this phase remains
	// a self-contained, append-only addition that's easy to audit and
	// revert. The handler's CreateScan method is unchanged in routing —
	// the *behavior* of POST /security/scans/ now also creates a
	// ClusterScan CR, but the route is the same.
	if deps.Security != nil {
		// Wire the optional CIS dependencies onto the existing handler. We
		// do this here (instead of touching server.go) because all of the
		// inputs are already available in `deps` and `cfg`. The handler
		// is nil-safe for any of these — when they're absent the legacy
		// (DB-only) code path remains intact.
		if deps.Hub != nil {
			deps.Security.SetK8sRequester(handler.NewTunnelK8sRequester(deps.Hub))
		}
		if deps.RemoteQueries != nil {
			deps.Security.SetClusterQuerier(deps.RemoteQueries)
			deps.Security.SetIngestPersister(deps.RemoteQueries)
		}
		// Optional asynq client wiring kept for parity with other handlers
		// — the in-process poller does the actual ingestion today, but a
		// queue connection is still useful for any future cross-process
		// triggers (e.g. webhook → enqueue).
		if cfg != nil && cfg.RedisURL != "" {
			if redisOpt, err := asynq.ParseRedisURI(cfg.RedisURL); err == nil {
				deps.Security.SetIngestQueue(asynq.NewClient(redisOpt))
			}
		}
		secGate := featureGate("feature.security", deps.SettingsCache)
		r.With(secGate).Get("/security/profiles/", deps.Security.ListProfiles)
		r.With(secGate).Get("/security/scans/{id}/", deps.Security.GetScanFull)
		r.With(secGate).Get("/security/scans/{id}/report.csv", deps.Security.ExportScanCSV)
	}

}
