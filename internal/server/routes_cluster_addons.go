package server

import (
	iauth "github.com/alphabravocompany/astronomer-go/internal/auth"
	"github.com/alphabravocompany/astronomer-go/internal/rbac"
	appmiddleware "github.com/alphabravocompany/astronomer-go/internal/server/middleware"
	"github.com/go-chi/chi/v5"
)

// Code organization: this file holds a domain-specific slice of the
// protected-route registration originally inlined in routes.go's
// registerProtectedRoutes. Pure behaviour-preserving extraction.

func registerClusterAddonRoutes(r chi.Router, deps RouterDependencies) {
	writeClusters := requireScope(iauth.ScopeWriteClusters)

	if deps.AdminPlatform.Extensions != nil {
		r.Group(func(r chi.Router) {
			// Opt-in: no marketplace yet, so the surface 404s until an operator
			// flips feature.extensions. Fail closed when the settings cache is
			// unwired (nil reader → 404), matching Charlie.
			r.Use(appmiddleware.FeatureGateDefault("feature.extensions", deps.CoreAuth.SettingsCache, false))
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Get("/extensions/", deps.AdminPlatform.Extensions.List)
			// §HostMounts — viewer-readable, render-only projection of enabled
			// extensions for the host loader. Not ScopeAdmin: any viewer may
			// render the extension surface.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Get("/extensions/mounts/", deps.AdminPlatform.Extensions.Mounts)
			// §DataProxy — tenant-user data path, NOT ScopeAdmin. ResourceSettings:read
			// just gates "may use the extensions surface at all"; the real, per-call
			// gate is the requesting user's own RBAC re-checked against the manifest's
			// declared dataSource inside ProxyData. An extension can never exceed the
			// user.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Post("/extensions/{name}/data/{dataSourceId}/", deps.AdminPlatform.Extensions.ProxyData)
			// §BridgeProtocol — Tier-2 scoped-token issuance backing the iframe's
			// ext/token.request. Viewer-gated (settings:read, NOT admin); the host
			// mints a short-lived, single-use, ≤60s ticket only after re-checking
			// the requesting user's own RBAC for the manifest-declared dataSource —
			// the same gate as §DataProxy. The iframe never receives the session JWT.
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Post("/extensions/{name}/token/", deps.AdminPlatform.Extensions.IssueTicket)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbRead)).Get("/extensions/sample-manifest/", deps.AdminPlatform.Extensions.SampleManifest)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/validate/", deps.AdminPlatform.Extensions.Validate)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/verify-bundle/", deps.AdminPlatform.Extensions.VerifyBundle)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/", deps.AdminPlatform.Extensions.Install)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/{name}/enable/", deps.AdminPlatform.Extensions.Enable)
			r.With(requireScope(iauth.ScopeAdmin), requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceSettings, rbac.VerbUpdate)).Post("/extensions/{name}/disable/", deps.AdminPlatform.Extensions.Disable)
		})
	}

	// Cluster templates (migration 049). Two mount points:
	//   - /cluster-templates/* — CRUD on templates, gated on the new
	//     cluster_templates resource so superusers and a dedicated
	//     "template administrator" role can manage them without
	//     requiring full clusters:write.
	//   - /clusters/{cluster_id}/template/* — bind/apply/detach, gated on
	//     ResourceClusters + VerbUpdate (the operator who can edit a
	//     cluster can apply a template to it).
	if deps.ClusterResources.ClusterTemplates != nil {
		r.Route("/cluster-templates", func(r chi.Router) {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbList)).Get("/", deps.ClusterResources.ClusterTemplates.List)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbCreate)).Post("/", deps.ClusterResources.ClusterTemplates.Create)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbRead)).Get("/{id}/", deps.ClusterResources.ClusterTemplates.Get)
			r.With(
				requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbRead),
				requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead),
			).Get("/{id}/clusters/", deps.ClusterResources.ClusterTemplates.ListBoundClusters)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterResources.ClusterTemplates.Update)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterResources.ClusterTemplates.Update)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusterTemplates, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterResources.ClusterTemplates.Delete)
		})
		// Per-cluster bind / status / reapply / detach.
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/template/", deps.ClusterResources.ClusterTemplates.Apply)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/template/", deps.ClusterResources.ClusterTemplates.GetApplication)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/template/reapply/", deps.ClusterResources.ClusterTemplates.Reapply)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/template/", deps.ClusterResources.ClusterTemplates.Detach)
	}

	// Network policy templates (migration 068). Two mount points:
	//   - /admin/network-policy-templates/* — superuser CRUD over the
	//     library. Builtin rows are read-only at the handler level.
	//   - /clusters/{cluster_id}/network-policies/applications/* — per-
	//     cluster apply/list/delete, gated on ResourceClusters +
	//     VerbUpdate (same authority as editing the cluster).
	if deps.ClusterResources.NetworkPolicies != nil {
		r.Route("/admin/network-policy-templates", func(r chi.Router) {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbList)).Get("/", deps.ClusterResources.NetworkPolicies.ListTemplates)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbCreate)).Post("/", deps.ClusterResources.NetworkPolicies.CreateTemplate)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbRead)).Get("/{id}/", deps.ClusterResources.NetworkPolicies.GetTemplate)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterResources.NetworkPolicies.UpdateTemplate)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceNetworkPolicies, rbac.VerbDelete)).Delete("/{id}/", deps.ClusterResources.NetworkPolicies.DeleteTemplate)
		})
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/network-policies/applications/", deps.ClusterResources.NetworkPolicies.ListApplications)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/network-policies/applications/", deps.ClusterResources.NetworkPolicies.CreateApplications)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/network-policies/applications/{id}/", deps.ClusterResources.NetworkPolicies.DeleteApplication)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/network-policies/applications/{id}/reapply/", deps.ClusterResources.NetworkPolicies.Reapply)
	}

	// Cluster registries (migration 050) — multi-registry-per-cluster admin
	// UX, mounted alongside the legacy /clusters/{id}/registry/ single-row
	// route. All endpoints are gated on the parent cluster's RBAC verb so
	// "admin who can edit cluster X" implicitly also manages X's registry
	// pull secrets.
	if deps.ClusterResources.ClusterRegistries != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/registries/", deps.ClusterResources.ClusterRegistries.List)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/registries/", deps.ClusterResources.ClusterRegistries.Create)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/registries/{id}/", deps.ClusterResources.ClusterRegistries.Get)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/registries/{id}/", deps.ClusterResources.ClusterRegistries.Update)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/registries/{id}/", deps.ClusterResources.ClusterRegistries.Delete)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/registries/{id}/test/", deps.ClusterResources.ClusterRegistries.Test)
	}

	// Cluster snapshots (migration 052) — per-cluster Velero
	// self-service. List/get are clusters:read; mutating ops are
	// clusters:update because the operator who can edit a cluster is
	// the same one who can snapshot it. The velero-status pre-flight
	// is clusters:read so the install-Velero CTA renders for any
	// reader.
	if deps.ClusterResources.ClusterSnapshots != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshots/", deps.ClusterResources.ClusterSnapshots.ListSnapshots)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshots/", deps.ClusterResources.ClusterSnapshots.CreateSnapshot)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshots/{id}/", deps.ClusterResources.ClusterSnapshots.GetSnapshot)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/snapshots/{id}/", deps.ClusterResources.ClusterSnapshots.DeleteSnapshot)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshots/{id}/restore/", deps.ClusterResources.ClusterSnapshots.CreateRestore)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshot-schedules/", deps.ClusterResources.ClusterSnapshots.ListSchedules)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/snapshot-schedules/", deps.ClusterResources.ClusterSnapshots.CreateSchedule)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterResources.ClusterSnapshots.GetSchedule)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterResources.ClusterSnapshots.UpdateSchedule)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/clusters/{cluster_id}/snapshot-schedules/{id}/", deps.ClusterResources.ClusterSnapshots.DeleteSchedule)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/velero-status/", deps.ClusterResources.ClusterSnapshots.VeleroStatus)
	}

	// Control-plane (etcd) DR snapshots (migration 125). Nil unless
	// control_plane_snapshots_enabled — trigger applies a PRIVILEGED node Job,
	// so it requires the cluster mutation grant plus explicit node-management
	// and pod-exec authority. The Job enters the host PID namespace and is
	// cluster-admin-equivalent; clusters:update alone is intentionally
	// insufficient.
	if deps.ClusterResources.ControlPlaneSnapshots != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/control-plane-snapshots/", deps.ClusterResources.ControlPlaneSnapshots.ListSnapshots)
		r.With(writeClusters, requireAllPermissions(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries,
			permissionRequirement{resource: rbac.ResourceClusters, verb: rbac.VerbUpdate},
			permissionRequirement{resource: rbac.ResourceNodes, verb: rbac.VerbManage},
			permissionRequirement{resource: rbac.ResourcePods, verb: rbac.VerbExec},
		)).Post("/clusters/{cluster_id}/control-plane-snapshots/", deps.ClusterResources.ControlPlaneSnapshots.TriggerSnapshot)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/control-plane-snapshots/{id}/", deps.ClusterResources.ControlPlaneSnapshots.GetSnapshot)
		// Restore is read-only guidance (an offline runbook — restore is never
		// performed through this API), so it's a GET gated on clusters:read.
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/control-plane-snapshots/{id}/restore-guidance/", deps.ClusterResources.ControlPlaneSnapshots.Restore)
	}

	// Apiserver allow-list (migration 070).
	if deps.ClusterResources.ApiserverAllowlist != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/", deps.ClusterResources.ApiserverAllowlist.Get)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/clusters/{cluster_id}/apiserver-allowlist/", deps.ClusterResources.ApiserverAllowlist.Update)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/apiserver-allowlist/reconcile/", deps.ClusterResources.ApiserverAllowlist.Reconcile)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/snapshots/", deps.ClusterResources.ApiserverAllowlist.Snapshots)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/apiserver-allowlist/preview/", deps.ClusterResources.ApiserverAllowlist.Preview)
	}

	// Service mesh tile (migration 071).
	if deps.ClusterResources.ServiceMesh != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/", deps.ClusterResources.ServiceMesh.Get)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Post("/clusters/{cluster_id}/service-mesh/detect/", deps.ClusterResources.ServiceMesh.Detect)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/mtls/", deps.ClusterResources.ServiceMesh.MTLS)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceServiceMesh, rbac.VerbRead)).Get("/clusters/{cluster_id}/service-mesh/inventory/", deps.ClusterResources.ServiceMesh.Inventory)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceServiceMesh, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/service-mesh/validate/", deps.ClusterResources.ServiceMesh.ValidatePolicy)
	}

	// In-browser kubectl shell (migration 065 / sprint 17). Every
	// cluster-scoped route is gated on clusters:update — opening a
	// privileged shell is a write action. The WS endpoint is mounted
	// on the same protected sub-router but skips the per-handler
	// rate limiter (it's a single long-lived connection, not a burst
	// vector — the underlying /api/v1/ws/exec/ ratelimiter still
	// applies on the redirect target). Admin views are superuser-only
	// inside the handler itself (matches admin_drill.go).
	if deps.StreamingInternal.KubectlShell != nil {
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/shell/sessions/", deps.StreamingInternal.KubectlShell.Open)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/", deps.StreamingInternal.KubectlShell.List)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/{id}/", deps.StreamingInternal.KubectlShell.Get)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/clusters/{cluster_id}/shell/sessions/{id}/close/", deps.StreamingInternal.KubectlShell.Close)
		r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/clusters/{cluster_id}/shell/sessions/{id}/commands/", deps.StreamingInternal.KubectlShell.Commands)
		// Admin views — gated by superuser-check inside the handler.
		r.Get("/admin/shell-sessions/", deps.StreamingInternal.KubectlShell.AdminListAll)
		r.Get("/admin/shell-sessions/{id}/commands/", deps.StreamingInternal.KubectlShell.AdminCommands)
	}

	// Cluster groups (migration 066). All routes gated by clusters:update
	// because group admin is a clusters-admin concept; the LIST/GET reads
	// are also gated to keep the boundary tight (operators who can't
	// administer clusters shouldn't see the operator-defined folder
	// structure either).
	if deps.ClusterResources.ClusterGroups != nil {
		r.Route("/cluster-groups", func(r chi.Router) {
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/", deps.ClusterResources.ClusterGroups.List)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/", deps.ClusterResources.ClusterGroups.Create)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/", deps.ClusterResources.ClusterGroups.Get)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Put("/{id}/", deps.ClusterResources.ClusterGroups.Update)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Patch("/{id}/", deps.ClusterResources.ClusterGroups.Update)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Delete("/{id}/", deps.ClusterResources.ClusterGroups.Delete)
			r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Get("/{id}/clusters/", deps.ClusterResources.ClusterGroups.ListClusters)
			r.With(writeClusters, requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbUpdate)).Post("/{id}/move/", deps.ClusterResources.ClusterGroups.MoveClusters)
		})
	}

	// Sprint 069 — CRD-mirror v2 cluster-detail read surface. The full
	// /network-policies/ path returns every mirrored NetworkPolicy
	// (managed + operator-created); the parallel sprint-068
	// /network-policies/applications/ path owns the astronomer-managed
	// subset.
	if deps.ClusterResources.ClusterResources != nil {
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/ingress-classes/", deps.ClusterResources.ClusterResources.ListIngressClasses)
		r.With(requirePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/gateway-classes/", deps.ClusterResources.ClusterResources.ListGatewayClasses)
		// These three narrow their own query to ?namespace= when it is set
		// (ListMirrored*ByNamespace), so the gate is evaluated against the same
		// value — naming a namespace can only shrink the page. The two
		// *-classes routes above are cluster-scoped and ignore it.
		r.With(requireQueryNamespacePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/network-policies/", deps.ClusterResources.ClusterResources.ListNetworkPolicies)
		r.With(requireQueryNamespacePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/resource-quotas/", deps.ClusterResources.ClusterResources.ListResourceQuotas)
		r.With(requireQueryNamespacePermission(deps.CoreAuth.RBACEngine, deps.CoreAuth.RBACQueries, rbac.ResourceClusters, rbac.VerbRead)).Get("/clusters/{cluster_id}/limit-ranges/", deps.ClusterResources.ClusterResources.ListLimitRanges)
	}

}
