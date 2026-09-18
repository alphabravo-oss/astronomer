package server

import (
	"net/http"

	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/config"
	"github.com/alphabravocompany/astronomer-go/internal/handler"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerSecurityRoutes(r chi.Router, cfg *config.Config, deps RouterDependencies, rateLimit func(appmiddleware.APIRateLimitClass) func(http.Handler) http.Handler) {
	if deps.AdminPlatform.Security != nil {
		// Per-route authorization for the complete security surface. Authentication
		// alone is never sufficient here: templates, policies and CIS findings are
		// fleet security data. Global reads require security:read; cluster-scoped
		// reads additionally resolve the {cluster_id} scope through clusters:read.
		// ApplyPolicy + CreateScan push config to a managed cluster through the
		// tunnel, so they also carry the write-clusters token-scope backstop.
		secRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSecurity, rbac.VerbRead)
		secCreate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSecurity, rbac.VerbCreate)
		secUpdate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSecurity, rbac.VerbUpdate)
		secDelete := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSecurity, rbac.VerbDelete)
		clusterRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		secWriteClusters := requireScope(iauth.ScopeWriteClusters)
		r.With(featureGate("feature.security", deps.CoreAuth.SettingsCache)).Route("/security", func(r chi.Router) {
			r.With(secRead).Get("/controller/status/", deps.AdminPlatform.Security.ControllerStatus)
			r.With(secRead).Get("/templates/", deps.AdminPlatform.Security.ListTemplates)
			r.With(secCreate).Post("/templates/", deps.AdminPlatform.Security.CreateTemplate)
			r.With(secRead).Get("/templates/{id}/", deps.AdminPlatform.Security.GetTemplate)
			r.With(secUpdate).Put("/templates/{id}/", deps.AdminPlatform.Security.UpdateTemplate)
			r.With(secDelete).Delete("/templates/{id}/", deps.AdminPlatform.Security.DeleteTemplate)
			r.With(secRead).Get("/policies/", deps.AdminPlatform.Security.ListPolicies)
			r.With(secCreate).Post("/policies/", deps.AdminPlatform.Security.CreatePolicy)
			r.With(secWriteClusters, secUpdate).Post("/policies/{id}/apply/", deps.AdminPlatform.Security.ApplyPolicy)
			r.With(secDelete).Delete("/policies/{id}/", deps.AdminPlatform.Security.DeletePolicy)
			r.With(secRead).Get("/scans/", deps.AdminPlatform.Security.ListAllScans)
			r.With(secWriteClusters, secCreate).Post("/scans/", deps.AdminPlatform.Security.CreateScan)
		})
		r.With(clusterRead).Get("/clusters/{cluster_id}/security/policy/", deps.AdminPlatform.Security.GetPolicy)
		r.With(clusterRead).Get("/clusters/{cluster_id}/security/scans/", deps.AdminPlatform.Security.ListScans)
		r.With(clusterRead).Get("/clusters/{cluster_id}/security/scans/{id}/", deps.AdminPlatform.Security.GetScan)
		r.With(featureGate("feature.security", deps.CoreAuth.SettingsCache), secWriteClusters, secUpdate, clusterRead).
			Post("/clusters/{cluster_id}/security/scans/{id}/cancel/", deps.AdminPlatform.Security.CancelScan)
	}

	// --- P1 item 7: kube-apiserver audit-event collection -----------------
	// The per-cluster agent POSTs batched audit.k8s.io events to the ingest
	// endpoint (a write, gated on the dedicated audit_ingest:create — NOT
	// cluster:update, so an agent's ingest token cannot also mint exec
	// tickets or drive k8s-proxy writes); operators read them back via the
	// list endpoint (cluster:read). Cluster-scoped, idempotent on
	// (cluster_id, auditID) so a re-delivered batch is a no-op.
	if deps.ClusterResources.ApiserverAudit != nil {
		auditIngest := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceAuditIngest, rbac.VerbCreate)
		auditRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		r.With(requireScope(iauth.ScopeWriteClusters), auditIngest).Post("/clusters/{cluster_id}/apiserver-audit/", deps.ClusterResources.ApiserverAudit.Ingest)
		r.With(auditRead).Get("/clusters/{cluster_id}/apiserver-audit/", deps.ClusterResources.ApiserverAudit.List)
	}

	// --- Sprint 062: image vulnerability scanning -------------------------
	// Cluster-scoped reads gate on cluster:read; the rescan mutation also
	// requires cluster:update plus the write-clusters token scope. The fleet rollup pair
	// gates on security:read. Cluster routes live OUTSIDE the `/security`
	// mount so the existing CIS-benchmark routes stay untouched. The fleet
	// routes are nested under `/security/vulnerabilities/` so they pair
	// naturally with the CIS surface in the dashboard.
	if deps.ClusterResources.ImageVulns != nil {
		ivClusterRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)
		ivClusterUpdate := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)
		ivWriteClusters := requireScope(iauth.ScopeWriteClusters)
		ivSecurityRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSecurity, rbac.VerbRead)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/summary/", deps.ClusterResources.ImageVulns.ClusterSummary)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/images/", deps.ClusterResources.ImageVulns.ClusterTopImages)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/reports/{id}/", deps.ClusterResources.ImageVulns.ClusterReportDetail)
		r.With(ivWriteClusters, ivClusterUpdate, ivClusterRead).Post("/clusters/{cluster_id}/vulnerabilities/rescan/", deps.ClusterResources.ImageVulns.ClusterRescan)
		// Sprint 081: scan history sparkline + latest-vs-prior diff +
		// CSV download. All three are read-only and gated by the same
		// cluster:read RBAC the rest of the vuln surface uses.
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/history/", deps.ClusterResources.ImageVulns.ClusterHistory)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/diff/", deps.ClusterResources.ImageVulns.ClusterDiff)
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/export.csv", deps.ClusterResources.ImageVulns.ClusterExportCSV)
		// Per-image snapshot timeline — powers the drawer "scan history"
		// panel so operators can see how a single workload's CVE counts
		// have moved over time, not just the cluster-wide aggregate.
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/reports/{report_id}/history/", deps.ClusterResources.ImageVulns.ReportHistory)
		// Live scan-in-progress indicator: in-flight trivy Jobs +
		// operator readiness via the k8s passthrough. The UI polls
		// every 3s when scans are running, every 30s otherwise.
		r.With(ivClusterRead).Get("/clusters/{cluster_id}/vulnerabilities/progress/", deps.ClusterResources.ImageVulns.ClusterProgress)
		r.With(ivSecurityRead).Get("/security/vulnerabilities/summary/", deps.ClusterResources.ImageVulns.FleetSummary)
		r.With(ivSecurityRead).Get("/security/vulnerabilities/top-clusters/", deps.ClusterResources.ImageVulns.FleetTopClusters)
	}

	// --- Phase A3: cross-cluster resource search ---------------------------
	// Single endpoint that fans a list query out across every active cluster
	// in parallel. The handler enforces a per-cluster timeout and concurrency
	// cap so a single slow cluster cannot block the whole response. The
	// response includes per-cluster errors + counts so the UI can surface
	// partial failures gracefully.
	if deps.ClusterResources.ResourcesSearch != nil {
		// Cross-cluster fan-out — each call hits every connected tunnel.
		// Rate limit per-user so a runaway typeahead can't DoS the fleet.
		r.With(rateLimit(appmiddleware.ClassSearch)).
			Get("/resources/search/", deps.ClusterResources.ResourcesSearch.Search)
	}

	// --- Phase B5: CIS scans via cis-operator ------------------------------
	// Mounts the CIS-specific routes layered over the existing security
	// handler. These routes live below the same `/security` prefix as the
	// pre-existing scan endpoints, but are registered here (instead of in
	// the main `if deps.AdminPlatform.Security != nil` block above) so this phase remains
	// a self-contained, append-only addition that's easy to audit and
	// revert. The handler's CreateScan method is unchanged in routing —
	// the *behavior* of POST /security/scans/ now also creates a
	// ClusterScan CR, but the route is the same.
	if deps.AdminPlatform.Security != nil {
		// Wire the optional CIS dependencies onto the existing handler. We
		// do this here (instead of touching server.go) because all of the
		// inputs are already available in `deps` and `cfg`. The handler
		// is nil-safe for any of these — when they're absent the legacy
		// (DB-only) code path remains intact.
		if deps.StreamingInternal.Hub != nil {
			deps.AdminPlatform.Security.SetK8sRequester(handler.NewTunnelK8sRequester(deps.StreamingInternal.Hub))
		}
		if deps.CoreAuth.Queries != nil {
			deps.AdminPlatform.Security.SetClusterQuerier(deps.CoreAuth.Queries)
		}
		secGate := featureGate("feature.security", deps.CoreAuth.SettingsCache)
		secRead := requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSecurity, rbac.VerbRead)
		r.With(secGate, secRead).Get("/security/profiles/", deps.AdminPlatform.Security.ListProfiles)
		r.With(secGate, secRead).Get("/security/scans/{id}/", deps.AdminPlatform.Security.GetScanFull)
		r.With(secGate, secRead).Get("/security/scans/{id}/report.csv", deps.AdminPlatform.Security.ExportScanCSV)
	}

}
