package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerClusterAddonRoutes(r chi.Router, deps RouterDependencies) {
	writeClusters := requireScope(iauth.ScopeWriteClusters)

	if deps.Extensions != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Get("/extensions/", deps.Extensions.List)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Get("/extensions/sample-manifest/", deps.Extensions.SampleManifest)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/validate/", deps.Extensions.Validate)
		r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/", deps.Extensions.Install)
		r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/{name}/enable/", deps.Extensions.Enable)
		r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/{name}/disable/", deps.Extensions.Disable)
	}

	// Cluster templates (migration 049). Two mount points:
	//   - /cluster-templates/* — CRUD on templates, gated on the new
	//     cluster_templates resource so superusers and a dedicated
	//     "template administrator" role can manage them without
	//     requiring full clusters:write.
	//   - /clusters/{cluster_id}/template/* — bind/apply/detach, gated on
	//     ResourceClusters + VerbUpdate (the operator who can edit a
	//     cluster can apply a template to it).
	if deps.ClusterTemplates != nil {
		r.Route("/cluster-templates", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbList)).Get("/", deps.ClusterTemplates.List)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbCreate)).Post("/", deps.ClusterTemplates.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbRead)).Get("/{id}/", deps.ClusterTemplates.Get)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterTemplates.Update)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterTemplates.Update)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterTemplates.Delete)
		})
		// Per-cluster bind / status / reapply / detach.
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/template/", deps.ClusterTemplates.Apply)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/template/", deps.ClusterTemplates.GetApplication)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/template/reapply/", deps.ClusterTemplates.Reapply)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/template/", deps.ClusterTemplates.Detach)
	}

	// Network policy templates (migration 068). Two mount points:
	//   - /admin/network-policy-templates/* — superuser CRUD over the
	//     library. Builtin rows are read-only at the handler level.
	//   - /clusters/{cluster_id}/network-policies/applications/* — per-
	//     cluster apply/list/delete, gated on ResourceClusters +
	//     VerbUpdate (same authority as editing the cluster).
	if deps.NetworkPolicies != nil {
		r.Route("/admin/network-policy-templates", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbList)).Get("/", deps.NetworkPolicies.ListTemplates)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbCreate)).Post("/", deps.NetworkPolicies.CreateTemplate)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbRead)).Get("/{id}/", deps.NetworkPolicies.GetTemplate)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbUpdate)).Put("/{id}/", deps.NetworkPolicies.UpdateTemplate)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbDelete)).Delete("/{id}/", deps.NetworkPolicies.DeleteTemplate)
		})
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/network-policies/applications/", deps.NetworkPolicies.ListApplications)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/network-policies/applications/", deps.NetworkPolicies.CreateApplications)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/network-policies/applications/{id}/", deps.NetworkPolicies.DeleteApplication)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/network-policies/applications/{id}/reapply/", deps.NetworkPolicies.Reapply)
	}

	// Cluster registries (migration 050) — multi-registry-per-cluster admin
	// UX, mounted alongside the legacy /clusters/{id}/registry/ single-row
	// route. All endpoints are gated on the parent cluster's RBAC verb so
	// "admin who can edit cluster X" implicitly also manages X's registry
	// pull secrets.
	if deps.ClusterRegistries != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/registries/", deps.ClusterRegistries.List)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/registries/", deps.ClusterRegistries.Create)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/registries/{id}/", deps.ClusterRegistries.Get)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/registries/{id}/", deps.ClusterRegistries.Update)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/registries/{id}/", deps.ClusterRegistries.Delete)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/registries/{id}/test/", deps.ClusterRegistries.Test)
	}

	// Cluster snapshots (migration 052) — per-cluster Velero
	// self-service. List/get are clusters:read; mutating ops are
	// clusters:update because the operator who can edit a cluster is
	// the same one who can snapshot it. The velero-status pre-flight
	// is clusters:read so the install-Velero CTA renders for any
	// reader.
	if deps.ClusterSnapshots != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshots/", deps.ClusterSnapshots.ListSnapshots)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshots/", deps.ClusterSnapshots.CreateSnapshot)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshots/{id}/", deps.ClusterSnapshots.GetSnapshot)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/snapshots/{id}/", deps.ClusterSnapshots.DeleteSnapshot)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshots/{id}/restore/", deps.ClusterSnapshots.CreateRestore)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshot-schedules/", deps.ClusterSnapshots.ListSchedules)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshot-schedules/", deps.ClusterSnapshots.CreateSchedule)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterSnapshots.GetSchedule)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterSnapshots.UpdateSchedule)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterSnapshots.DeleteSchedule)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/velero-status/", deps.ClusterSnapshots.VeleroStatus)
	}

	// Apiserver allow-list (migration 070).
	if deps.ApiserverAllowlist != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/", deps.ApiserverAllowlist.Get)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/apiserver-allowlist/", deps.ApiserverAllowlist.Update)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/apiserver-allowlist/reconcile/", deps.ApiserverAllowlist.Reconcile)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/snapshots/", deps.ApiserverAllowlist.Snapshots)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/preview/", deps.ApiserverAllowlist.Preview)
	}

	// Fleet operations (migration 056).
	if deps.FleetOperations != nil {
		r.Route("/fleet-operations", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbList)).Get("/", deps.FleetOperations.List)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbCreate)).Post("/", deps.FleetOperations.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbRead)).Get("/{id}/", deps.FleetOperations.Get)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbRead)).Get("/{id}/targets/", deps.FleetOperations.ListTargets)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/pause/", deps.FleetOperations.Pause)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/resume/", deps.FleetOperations.Resume)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/abort/", deps.FleetOperations.Abort)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceFleetOperations, rbac.VerbUpdate)).Post("/{id}/retry-failed/", deps.FleetOperations.RetryFailed)
		})
	}

	// Service mesh tile (migration 071).
	if deps.ServiceMesh != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/", deps.ServiceMesh.Get)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/clusters/{cluster_id}/service-mesh/detect/", deps.ServiceMesh.Detect)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/mtls/", deps.ServiceMesh.MTLS)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceServiceMesh, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/inventory/", deps.ServiceMesh.Inventory)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceServiceMesh, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/service-mesh/validate/", deps.ServiceMesh.ValidatePolicy)
	}

	// In-browser kubectl shell (migration 065 / sprint 17). Every
	// cluster-scoped route is gated on clusters:update — opening a
	// privileged shell is a write action. The WS endpoint is mounted
	// on the same protected sub-router but skips the per-handler
	// rate limiter (it's a single long-lived connection, not a burst
	// vector — the underlying /api/v1/ws/exec/ ratelimiter still
	// applies on the redirect target). Admin views are superuser-only
	// inside the handler itself (matches admin_drill.go).
	if deps.KubectlShell != nil {
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/shell/sessions/", deps.KubectlShell.Open)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/", deps.KubectlShell.List)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/{id}/", deps.KubectlShell.Get)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/shell/sessions/{id}/close/", deps.KubectlShell.Close)
		r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/{id}/commands/", deps.KubectlShell.Commands)
		// Admin views — gated by superuser-check inside the handler.
		r.Get("/admin/shell-sessions/", deps.KubectlShell.AdminListAll)
		r.Get("/admin/shell-sessions/{id}/commands/", deps.KubectlShell.AdminCommands)
	}

	// Cluster groups (migration 066). All routes gated by clusters:update
	// because group admin is a clusters-admin concept; the LIST/GET reads
	// are also gated to keep the boundary tight (operators who can't
	// administer clusters shouldn't see the operator-defined folder
	// structure either).
	if deps.ClusterGroups != nil {
		r.Route("/cluster-groups", func(r chi.Router) {
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/", deps.ClusterGroups.List)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/", deps.ClusterGroups.Create)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/", deps.ClusterGroups.Get)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterGroups.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterGroups.Update)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/{id}/", deps.ClusterGroups.Delete)
			r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/clusters/", deps.ClusterGroups.ListClusters)
			r.With(writeClusters, requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/move/", deps.ClusterGroups.MoveClusters)
		})
	}

	// Sprint 069 — CRD-mirror v2 cluster-detail read surface. The full
	// /network-policies/ path returns every mirrored NetworkPolicy
	// (managed + operator-created); the parallel sprint-068
	// /network-policies/applications/ path owns the astronomer-managed
	// subset.
	if deps.ClusterResources != nil {
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/ingress-classes/", deps.ClusterResources.ListIngressClasses)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/gateway-classes/", deps.ClusterResources.ListGatewayClasses)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/network-policies/", deps.ClusterResources.ListNetworkPolicies)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/resource-quotas/", deps.ClusterResources.ListResourceQuotas)
		r.With(requirePermission(deps.RBACEngine, deps.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/limit-ranges/", deps.ClusterResources.ListLimitRanges)
	}

}
