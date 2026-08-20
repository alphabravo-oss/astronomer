'use client';

/**
 * /dashboard/clusters/$id/monitoring-stack — lifecycle for ONE cluster's
 * kube-prometheus-stack. It sits in the cluster subtree, next to Tools and
 * Apps, because that is where an operator managing this cluster looks for
 * "what is installed on it and how do I change that".
 *
 * PERMISSION SCOPE. Despite living under /clusters/{id}, these routes are
 * evaluated at GLOBAL scope by the server, so the decisions below are too —
 * see the comment on `scope`.
 */
import { Link } from '@/lib/link';
import { ArrowLeft, BarChart3, ExternalLink } from 'lucide-react';

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
import { CLUSTER_STACK_FAMILY, fleetGrafanaClusterURL } from '@/components/monitoring/stack-spec';
import { useSharedGrafanaStatus, useSharedThanosStatus } from '@/components/monitoring/hooks';
import type { SharedGrafanaStatus, SharedThanosStatus } from '@/lib/api/monitoring-stack';

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
  const thanosQuery = useSharedThanosStatus(permissions.read.allowed);
  const thanos = thanosQuery.data as SharedThanosStatus | undefined;
  const sharedThanosStorageId =
    thanos?.status === 'healthy' ? (thanos.storageConfigId || '').trim() : '';
  const grafanaQuery = useSharedGrafanaStatus(permissions.read.allowed);
  const grafanaOpenURL = fleetGrafanaClusterURL(
    grafanaQuery.data as SharedGrafanaStatus | undefined,
    clusterId,
  );

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
          <div className="flex items-center gap-2">
            <Link
              href={`/dashboard/clusters/${clusterId}/metrics`}
              className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              <BarChart3 className="h-3.5 w-3.5" />
              Metrics
            </Link>
            <Link
              href="/dashboard/settings/monitoring"
              className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            >
              Shared stacks
            </Link>
          </div>
        }
      />

      {!permissions.read.allowed ? (
        <PermissionState title="Monitoring access required" permission="monitoring:read" />
      ) : (
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground" data-testid="two-grafana-copy">
            Cluster Grafana talks to this cluster’s Prometheus (15d local retention) and
            survives an Astronomer outage. Fleet Grafana is the lobby for comparing
            clusters, long-term metrics, and logs — it dies with Astronomer. We do not
            uninstall cluster Grafana automatically.
            {grafanaOpenURL ? (
              <>
                {' '}
                <a
                  href={grafanaOpenURL}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 font-medium text-foreground hover:underline"
                >
                  Open fleet Grafana
                  <ExternalLink className="h-3 w-3" />
                </a>
              </>
            ) : null}
          </p>

          {sharedThanosStorageId ? (
            <p className="text-sm text-muted-foreground" data-testid="shared-thanos-bucket-prefill">
              Use shared Thanos bucket. Object storage is pre-filled from the healthy
              shared Thanos stack. This does not enable Thanos Receive or remote_write.
            </p>
          ) : null}

          <StackLifecyclePanel
            target={target}
            spec={CLUSTER_STACK_FAMILY}
            permissions={permissions}
            storageOptions={storageOptions}
            seedOverrides={sharedThanosStorageId ? { storageConfigId: sharedThanosStorageId } : undefined}
          />
        </div>
      )}
    </PageShell>
  );
}
