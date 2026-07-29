'use client';

/**
 * /dashboard/clusters/$id/monitoring-stack — lifecycle for ONE cluster's
 * kube-prometheus-stack. It sits in the cluster subtree, next to Tools and
 * Apps, because that is where an operator managing this cluster looks for
 * "what is installed on it and how do I change that".
 *
 * ⚠ THIS PAGE IS BLOCKED ON A ONE-LINE BACKEND FIX, and says so on screen.
 *
 * All six per-cluster handlers read `chi.URLParam(r, "cluster_id")` —
 * internal/handler/monitoring_stack_cluster.go:265 (UninstallStack), :315
 * (GetStackStatus) and :393 (monitoringStackPayload, backing preview / install /
 * upgrade / replace) — but every route is mounted as
 * `/{id}/monitoring/stack/...` at internal/server/routes_clusters.go:83-88 and
 * no enclosing pattern declares `{cluster_id}`. The param is therefore always
 * empty in production: status / install / upgrade / replace / uninstall answer
 * >= 400 and preview 200s with an empty `clusterId`.
 *
 * That defect is PINNED, deliberately, by
 * internal/handler/monitoring_stack_test.go:410-465
 * (TestClusterStackClusterIDParamIsUnroutable) — it passes today, and it fails
 * the moment the fix lands, which is the signal to delete the notice below.
 * The fix is `chi.URLParam(r, "id")` at those three sites. No Go was in scope
 * for this change, so the page names the problem rather than presenting
 * controls that look like they work. The shared-stack page is unaffected.
 *
 * PERMISSION SCOPE. Despite living under /clusters/{id}, these routes are
 * evaluated at GLOBAL scope by the server, so the decisions below are too —
 * see the comment on `scope`.
 */
import { Link } from '@/lib/link';
import { ArrowLeft, BarChart3, AlertTriangle } from 'lucide-react';

import { PageHeader, PageShell } from '@/components/ui/page';
import { PermissionState } from '@/components/ui/empty-state';
import { usePermissionDecision } from '@/lib/permission-hooks';
import { useCluster } from '@/lib/hooks';
import { useB2StorageLocations } from '@/components/backups/hooks';
import {
  StackLifecyclePanel,
  type StackLifecyclePermissions,
  type StackOption,
} from '@/components/monitoring/stack-lifecycle-panel';
import { CLUSTER_STACK_FAMILY } from '@/components/monitoring/stack-spec';

export function ClusterMonitoringStackPage({ clusterId }: { clusterId: string }) {
  /**
   * GLOBAL, not `{ type: 'cluster', id: clusterId }`, even though the routes are
   * mounted under /clusters/{id}.
   *
   * RequirePermission only falls back to the `{id}` route param when the
   * resource is rbac.ResourceClusters (internal/server/middleware/rbac.go:92-99).
   * For rbac.ResourceMonitoring it reads `chi.URLParam(r, "cluster_id")`, which
   * is empty here, so clusterID is uuid.Nil and bindingApplies
   * (internal/rbac/engine.go:371-393) rejects every cluster-scoped binding.
   * explainPermission on the client WOULD honour a cluster-scoped binding
   * (src/lib/permissions.ts:116, :148-166), so asking at cluster scope makes the
   * UI strictly more permissive than the API: a user holding monitoring:create
   * on just this cluster would be shown Install and then 403d — the exact
   * opposite of this feature's "absent, not disabled" policy.
   *
   * Revisit when the backend decides whether monitoring bindings should be
   * cluster-scopable; the same follow-up as the routing fix above
   * (internal/handler/monitoring_stack_test.go:418-423 records both).
   */
  const scope = { type: 'global' as const };
  const permissions: StackLifecyclePermissions = {
    read: usePermissionDecision('monitoring', 'read', scope),
    install: usePermissionDecision('monitoring', 'create', scope),
    update: usePermissionDecision('monitoring', 'update', scope),
    uninstall: usePermissionDecision('monitoring', 'delete', scope),
  };

  const { data: cluster } = useCluster(clusterId);
  const storageQuery = useB2StorageLocations();
  const storageOptions: StackOption[] = (storageQuery.data?.data ?? []).map((location) => ({
    id: location.id,
    label: `${location.name} — ${location.bucket}`,
  }));

  const target = { kind: 'cluster' as const, clusterId };

  return (
    <PageShell>
      <PageHeader
        eyebrow={
          <Link
            href={`/dashboard/clusters/${clusterId}`}
            className="inline-flex items-center gap-1 hover:text-foreground"
          >
            <ArrowLeft className="h-3 w-3" />
            {cluster?.displayName || 'Cluster'}
          </Link>
        }
        title="Monitoring stack"
        description="kube-prometheus-stack for this cluster. Install, upgrade, replace or uninstall the release; the panel follows the queued operation to completion and surfaces the reconciler's own errors."
        actions={
          <Link
            href="/dashboard/settings/monitoring"
            className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <BarChart3 className="h-3.5 w-3.5" />
            Shared stacks
          </Link>
        }
      />

      {!permissions.read.allowed ? (
        <PermissionState title="Monitoring access required" permission="monitoring:read" />
      ) : (
        <div className="space-y-4">
          {/*
            Delete this notice together with the header comment when
            TestClusterStackClusterIDParamIsUnroutable
            (internal/handler/monitoring_stack_test.go:410-465) starts failing —
            that is the signal the `chi.URLParam(r, "id")` fix has landed.
          */}
          <div
            role="alert"
            data-testid="cluster-stack-routing-blocked"
            className="flex items-start gap-2 rounded-md border border-status-error/30 bg-status-error/10 px-3 py-2 text-xs text-status-error"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <div className="min-w-0 space-y-1">
              <p className="font-medium">
                Per-cluster monitoring stack endpoints are not reachable on this server yet.
              </p>
              <p>
                The six handlers behind this page look the cluster up under a route parameter the
                router never binds, so status, install, upgrade, replace and uninstall answer an
                error and preview renders without a cluster. The controls below are shown so the
                page is reviewable, but they will not succeed until the backend fix ships. Use{' '}
                <Link href="/dashboard/settings/monitoring" className="underline">
                  Shared monitoring stacks
                </Link>{' '}
                — Thanos and Alertmanager are unaffected.
              </p>
            </div>
          </div>

          <StackLifecyclePanel
            target={target}
            spec={CLUSTER_STACK_FAMILY}
            permissions={permissions}
            storageOptions={storageOptions}
          />
        </div>
      )}
    </PageShell>
  );
}
