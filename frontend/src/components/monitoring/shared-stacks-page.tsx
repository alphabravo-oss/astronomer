'use client';

/**
 * /dashboard/settings/monitoring — lifecycle for the two SHARED monitoring
 * stacks: Thanos (long-term metrics) and Alertmanager (alert routing). Both run
 * on one management cluster and serve the whole fleet, which is why they live
 * under settings rather than on a cluster.
 *
 * Not wrapped in SettingsAuthGate. That gate is superuser-only, and these
 * endpoints are not: the backend authorizes them with
 * `authorizeGlobalAction(rbac.ResourceMonitoring, ...)`, so a monitoring
 * administrator who is not a platform superuser can legitimately use this page.
 * Gating it on superuser would lock out callers the API accepts.
 *
 * Permission mapping, from internal/handler/monitoring_stack_shared.go — note
 * that the shared families do NOT use create/delete the way the per-cluster
 * stack does. Every mutating verb, install and uninstall included, is
 * monitoring:update at GLOBAL scope.
 */
import { Link } from '@/lib/link';
import { ArrowLeft, BarChart3 } from 'lucide-react';

import { PageHeader, PageShell } from '@/components/ui/page';
import { PermissionState } from '@/components/ui/empty-state';
import { usePermissionDecision } from '@/lib/permission-hooks';
import { useClusters } from '@/lib/hooks';
import { useB2StorageLocations } from '@/components/backups/hooks';
import {
  StackLifecyclePanel,
  type StackLifecyclePermissions,
  type StackOption,
} from '@/components/monitoring/stack-lifecycle-panel';
import {
  SHARED_ALERTMANAGER_FAMILY,
  SHARED_THANOS_FAMILY,
} from '@/components/monitoring/stack-spec';

const THANOS_TARGET = { kind: 'thanos' } as const;
const ALERTMANAGER_TARGET = { kind: 'alertmanager' } as const;

export function SharedMonitoringStacksPage() {
  const read = usePermissionDecision('monitoring', 'read');
  const update = usePermissionDecision('monitoring', 'update');
  // install / uninstall are monitoring:update on the shared families — the
  // create/delete split only exists on the per-cluster routes.
  const permissions: StackLifecyclePermissions = {
    read,
    install: update,
    update,
    uninstall: update,
  };

  const clustersQuery = useClusters({ pageSize: 100 });
  // Backup storage configs double as the object-storage source for Thanos
  // (the handler resolves storageConfigId through GetBackupStorageConfigByID).
  // A caller without backups:read gets an empty list; the field then degrades
  // to a free-text id input rather than blocking the form.
  const storageQuery = useB2StorageLocations();

  const clusters = clustersQuery.data?.data ?? [];
  const clusterOptions: StackOption[] = clusters.map((cluster) => ({
    id: cluster.id,
    label: cluster.isLocal
      ? `${cluster.displayName || cluster.name} (management)`
      : cluster.displayName || cluster.name,
  }));
  const storageOptions: StackOption[] = (storageQuery.data?.data ?? []).map((location) => ({
    id: location.id,
    label: `${location.name} — ${location.bucket}`,
  }));

  const managementClusterId = clusters.find((cluster) => cluster.isLocal)?.id ?? '';
  const seedOverrides = managementClusterId ? { managementClusterId } : undefined;

  return (
    <PageShell>
      <PageHeader
        eyebrow={
          <Link
            href="/dashboard/settings"
            className="inline-flex items-center gap-1 hover:text-foreground"
          >
            <ArrowLeft className="h-3 w-3" />
            Settings
          </Link>
        }
        title="Shared monitoring stacks"
        description="Install, upgrade, replace and uninstall the fleet-wide Thanos and Alertmanager releases. Every action is queued and reconciled server-side — the panels below follow it to completion."
        actions={
          <Link
            href="/dashboard/monitoring"
            className="inline-flex h-8 items-center gap-2 rounded-md border border-border px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <BarChart3 className="h-3.5 w-3.5" />
            Metrics
          </Link>
        }
      />

      {!read.allowed ? (
        <PermissionState title="Monitoring access required" permission="monitoring:read" />
      ) : (
        <div className="space-y-4">
          <StackLifecyclePanel
            target={THANOS_TARGET}
            spec={SHARED_THANOS_FAMILY}
            permissions={permissions}
            clusterOptions={clusterOptions}
            storageOptions={storageOptions}
            seedOverrides={seedOverrides}
          />
          <StackLifecyclePanel
            target={ALERTMANAGER_TARGET}
            spec={SHARED_ALERTMANAGER_FAMILY}
            permissions={permissions}
            clusterOptions={clusterOptions}
            seedOverrides={seedOverrides}
          />
        </div>
      )}
    </PageShell>
  );
}
