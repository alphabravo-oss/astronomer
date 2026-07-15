import { createFileRoute } from '@tanstack/react-router';
import { useCallback, useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import {
  useCluster,
  useClusterNodes,
  useClusterNamespaces,
  useClusterEvents,
  useClusterPods,
  useWorkloads,
  useDeletePod,
  useScaleWorkload,
  useRestartWorkload,
  useServices,
  useIngresses,
  useNetworkPolicies,
  usePersistentVolumes,
  usePersistentVolumeClaims,
  useStorageClasses,
  useGenericResources,
  useGateways,
  useHTTPRoutes,
  useGatewayClasses,
  useGRPCRoutes,
  useTLSRoutes,
  useTCPRoutes,
  useUDPRoutes,
  useReferenceGrants,
  useDeleteService,
  useDeleteIngress,
  useDeleteNetworkPolicy,
  useDeletePV,
  useDeletePVC,
  useK8sDelete,
  useK8sPatch,
  queryKeys,
} from '@/lib/hooks';
import { useLiveQueryInvalidation } from '@/lib/live/hooks';
import { useResourceWatchInvalidation } from '@/hooks/use-resource-watch';
import * as apiClient from '@/lib/api';
import { StatusBadge } from '@/components/ui/status-badge';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ActionMenu, type ActionMenuItem } from '@/components/ui/action-menu';
import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { ScaleDialog } from '@/components/workloads/scale-dialog';
import { useWindowManagerStore } from '@/lib/window-manager-store';
import { YamlViewDialog } from '@/components/ui/yaml-view-dialog';
import { CreateResourceDialog } from '@/components/resources/create-resource-dialog';
import { ConfigMapFormDialog } from '@/components/resources/configmap-form';
import { k8sResourcePath, k8sListPath, getResourceDef, detailHref, kindToResourceType, WORKLOAD_SCALABLE_KINDS } from '@/lib/k8s-paths';
import { usePermissionDecision, canonicalPermissionResource } from '@/lib/permission-hooks';
import { formatBytes, formatCPU, formatRelativeTime, cn } from '@/lib/utils';
import type { PermissionDecision } from '@/lib/permissions';
import type {
  ClusterNode,
  Namespace,
  ClusterEvent,
  Pod,
  Workload,
  K8sService,
  Ingress,
  NetworkPolicy,
  PersistentVolume,
  PersistentVolumeClaim,
  StorageClass,
  GenericK8sResource,
  Gateway,
  GatewayClass,
  GatewayRoute,
  ReferenceGrant,
} from '@/types';
import { useRouter } from '@/lib/navigation';
import { Link } from '@/lib/link';
import {
  Loader2, Server, Terminal, FileText, Trash2, RotateCw, Scaling,
  Code, Pencil, ShieldBan, ShieldCheck, Unplug, Plus,
} from 'lucide-react';
import { toastApiError, toastError, toastSuccess, toastWarning } from '@/lib/toast';

/** Bespoke workload detail route (workloads keep their own detail page, not the generic one). */
function workloadDetailHref(clusterId: string, kind: string, namespace: string, name: string): string {
  return `/dashboard/clusters/${clusterId}/workloads/${kind.toLowerCase()}/${namespace}/${name}`;
}

// ── Drill-down helpers (GATE A) ──
//
// Tiny shared pieces so every resource table reads as clickable: a name cell
// rendered as a real <Link> (supports open-in-new-tab) and a wrapper that stops
// row-click propagation around per-row action controls.

/** A visible name link into the resource detail route; clicking it must not also fire the row click. */
function NameLink({ clusterId, resourceType, namespace, name }: {
  clusterId: string; resourceType: string; namespace?: string; name: string;
}) {
  return (
    <Link
      href={detailHref(clusterId, resourceType, namespace, name)}
      onClick={(e) => e.stopPropagation()}
      className="font-medium text-foreground font-mono text-xs hover:underline"
    >
      {name}
    </Link>
  );
}

/** Wrap per-row action controls so clicking them doesn't trigger the row drill-down. */
function StopRowClick({ children }: { children: React.ReactNode }) {
  return <div onClick={(e) => e.stopPropagation()}>{children}</div>;
}

/** A "Name" column whose cell links into the detail route, for any row carrying name/namespace. */
function nameColumn<T extends { name: string; namespace?: string }>(
  clusterId: string,
  resourceType: string,
): Column<T> {
  return {
    key: 'name',
    header: 'Name',
    accessor: (row) => (
      <NameLink clusterId={clusterId} resourceType={resourceType} namespace={row.namespace} name={row.name} />
    ),
    sortAccessor: (row) => row.name,
  };
}

/**
 * Build a read-gated onRowClick that drills into the resource detail route.
 * Mirrors the existing nodes onRowClick.
 */
function makeRowClick<T extends { name: string; namespace?: string }>(
  router: ReturnType<typeof useRouter>,
  clusterId: string,
  resourceType: string,
  read: PermissionDecision,
) {
  return (row: T) => {
    if (!read.allowed) {
      toastPermissionDenied(read);
      return;
    }
    router.push(detailHref(clusterId, resourceType, row.namespace, row.name));
  };
}

// ── Column Definitions ──

const nodeColumns: Column<ClusterNode>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: 'roles',
    header: 'Roles',
    accessor: (row) => (
      <div className="flex gap-1">
        {row.roles.map((role) => (
          <span key={role} className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">{role}</span>
        ))}
      </div>
    ),
  },
  {
    key: 'cpu',
    header: 'CPU',
    accessor: (row) => {
      const pct = row.cpuCapacity > 0 ? (row.cpuUsage / row.cpuCapacity) * 100 : 0;
      return (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={cn('gauge-bar-fill', pct >= 90 ? 'bg-status-error' : pct >= 75 ? 'bg-status-warning' : 'bg-status-success')}
              style={{ width: `${Math.min(pct, 100)}%` }}
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatCPU(row.cpuUsage)} / {formatCPU(row.cpuCapacity)}
          </span>
        </div>
      );
    },
    sortAccessor: (row) => row.cpuUsage,
  },
  {
    key: 'memory',
    header: 'Memory',
    accessor: (row) => {
      const pct = row.memoryCapacity > 0 ? (row.memoryUsage / row.memoryCapacity) * 100 : 0;
      return (
        <div className="flex items-center gap-2">
          <div className="w-16 gauge-bar">
            <div
              className={cn('gauge-bar-fill', pct >= 90 ? 'bg-status-error' : pct >= 75 ? 'bg-status-warning' : 'bg-status-success')}
              style={{ width: `${Math.min(pct, 100)}%` }}
            />
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {formatBytes(row.memoryUsage)} / {formatBytes(row.memoryCapacity)}
          </span>
        </div>
      );
    },
    sortAccessor: (row) => row.memoryUsage,
  },
  {
    key: 'pods',
    header: 'Pods',
    accessor: (row) => (
      <span className="text-muted-foreground tabular-nums text-xs">{row.podCount}/{row.podCapacity}</span>
    ),
    sortAccessor: (row) => row.podCount,
    align: 'center',
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const nsColumns: Column<Namespace>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: 'pods',
    header: 'Pods',
    accessor: (row) => <span className="tabular-nums">{row.podCount}</span>,
    sortAccessor: (row) => row.podCount,
    align: 'center',
  },
  {
    key: 'cpu',
    header: 'CPU Usage',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {formatCPU(row.cpuUsage)}{row.cpuLimit > 0 ? ` / ${formatCPU(row.cpuLimit)}` : ''}
      </span>
    ),
    sortAccessor: (row) => row.cpuUsage,
  },
  {
    key: 'memory',
    header: 'Memory Usage',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {formatBytes(row.memoryUsage)}{row.memoryLimit > 0 ? ` / ${formatBytes(row.memoryLimit)}` : ''}
      </span>
    ),
    sortAccessor: (row) => row.memoryUsage,
  },
  {
    key: 'created',
    header: 'Created',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const eventColumns: Column<ClusterEvent>[] = [
  {
    key: 'type',
    header: 'Type',
    accessor: (row) => (
      <span className={cn('text-xs font-medium', row.type === 'Warning' ? 'text-status-warning' : 'text-status-info')}>
        {row.type}
      </span>
    ),
  },
  {
    key: 'reason',
    header: 'Reason',
    accessor: (row) => <span className="font-medium text-foreground text-xs">{row.reason}</span>,
  },
  {
    key: 'object',
    header: 'Object',
    accessor: (row) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.involvedObject.kind}/{row.involvedObject.name}
      </span>
    ),
  },
  {
    key: 'message',
    header: 'Message',
    accessor: (row) => <span className="text-xs text-muted-foreground line-clamp-2">{row.message}</span>,
    sortable: false,
  },
  {
    key: 'count',
    header: 'Count',
    accessor: (row) => <span className="tabular-nums text-xs">{row.count}</span>,
    sortAccessor: (row) => row.count,
    align: 'center',
  },
  {
    key: 'lastSeen',
    header: 'Last Seen',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.lastTimestamp)}</span>,
  },
];

const podColumns: Column<Pod>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: 'ready',
    header: 'Ready',
    accessor: (row) => <span className="tabular-nums text-xs">{row.ready}</span>,
    align: 'center',
  },
  {
    key: 'restarts',
    header: 'Restarts',
    accessor: (row) => (
      <span className={cn('tabular-nums text-xs', row.restarts > 0 ? 'text-status-warning' : 'text-muted-foreground')}>
        {row.restarts}
      </span>
    ),
    sortAccessor: (row) => row.restarts,
    align: 'center',
  },
  {
    key: 'node',
    header: 'Node',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.node}</span>,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.age}</span>,
  },
];

const workloadColumns: Column<Workload>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'ready',
    header: 'Ready',
    accessor: (row) => <span className="tabular-nums text-xs">{row.ready}</span>,
    align: 'center',
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: 'images',
    header: 'Image',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
        {row.images?.[0] || '-'}
      </span>
    ),
    sortable: false,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.age}</span>,
  },
];

const serviceColumns: Column<K8sService>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'type',
    header: 'Type',
    accessor: (row) => (
      <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">{row.type}</span>
    ),
  },
  {
    key: 'clusterIP',
    header: 'Cluster IP',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.clusterIP}</span>,
  },
  {
    key: 'ports',
    header: 'Ports',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground tabular-nums">
        {row.ports?.map((p) => `${p.port}/${p.protocol}`).join(', ') || '-'}
      </span>
    ),
    sortable: false,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const ingressColumns: Column<Ingress>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'class',
    header: 'Class',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.ingressClass || '-'}</span>,
  },
  {
    key: 'hosts',
    header: 'Hosts',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
        {row.hosts?.join(', ') || '*'}
      </span>
    ),
    sortable: false,
  },
  {
    key: 'tls',
    header: 'TLS',
    accessor: (row) => (
      <span className={cn('text-xs', row.tls ? 'text-status-success' : 'text-muted-foreground')}>
        {row.tls ? 'Yes' : 'No'}
      </span>
    ),
    align: 'center',
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const networkPolicyColumns: Column<NetworkPolicy>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'policyTypes',
    header: 'Policy Types',
    accessor: (row) => (
      <div className="flex gap-1">
        {row.policyTypes?.map((t) => (
          <span key={t} className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">{t}</span>
        ))}
      </div>
    ),
    sortable: false,
  },
  {
    key: 'ingress',
    header: 'Ingress Rules',
    accessor: (row) => <span className="tabular-nums text-xs">{row.ingressRules}</span>,
    align: 'center',
  },
  {
    key: 'egress',
    header: 'Egress Rules',
    accessor: (row) => <span className="tabular-nums text-xs">{row.egressRules}</span>,
    align: 'center',
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const pvColumns: Column<PersistentVolume>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: 'capacity',
    header: 'Capacity',
    accessor: (row) => <span className="text-xs text-muted-foreground tabular-nums">{row.capacity}</span>,
  },
  {
    key: 'accessModes',
    header: 'Access Modes',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.accessModes?.join(', ')}</span>,
    sortable: false,
  },
  {
    key: 'storageClass',
    header: 'Storage Class',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.storageClass || '-'}</span>,
  },
  {
    key: 'claimRef',
    header: 'Claim',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.claimRef || '-'}</span>,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const pvcColumns: Column<PersistentVolumeClaim>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => <StatusBadge status={row.status} />,
  },
  {
    key: 'capacity',
    header: 'Capacity',
    accessor: (row) => <span className="text-xs text-muted-foreground tabular-nums">{row.capacity}</span>,
  },
  {
    key: 'storageClass',
    header: 'Storage Class',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.storageClass || '-'}</span>,
  },
  {
    key: 'volumeName',
    header: 'Volume',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.volumeName || '-'}</span>,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const storageClassColumns: Column<StorageClass>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => (
      <div className="flex items-center gap-2">
        <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>
        {row.isDefault && (
          <span className="px-1.5 py-0.5 rounded text-2xs bg-status-info/10 text-status-info">default</span>
        )}
      </div>
    ),
  },
  {
    key: 'provisioner',
    header: 'Provisioner',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.provisioner}</span>,
  },
  {
    key: 'reclaimPolicy',
    header: 'Reclaim Policy',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.reclaimPolicy}</span>,
  },
  {
    key: 'volumeBindingMode',
    header: 'Binding Mode',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.volumeBindingMode}</span>,
  },
  {
    key: 'expansion',
    header: 'Expansion',
    accessor: (row) => (
      <span className={cn('text-xs', row.allowVolumeExpansion ? 'text-status-success' : 'text-muted-foreground')}>
        {row.allowVolumeExpansion ? 'Allowed' : 'No'}
      </span>
    ),
    align: 'center',
  },
];

// ── Generic resource column definitions ──

const jobColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'status', header: 'Status', accessor: (row) => <StatusBadge status={row.status || 'Pending'} /> },
  { key: 'completions', header: 'Completions', accessor: (row) => <span className="tabular-nums text-xs">{row.succeeded ?? 0}/{row.completions ?? 1}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const cronJobColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'schedule', header: 'Schedule', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.schedule}</span> },
  { key: 'status', header: 'Status', accessor: (row) => <StatusBadge status={row.status || 'Active'} /> },
  { key: 'lastSchedule', header: 'Last Schedule', accessor: (row) => <span className="text-xs text-muted-foreground">{row.lastSchedule ? formatRelativeTime(row.lastSchedule) : '-'}</span> },
  { key: 'active', header: 'Active', accessor: (row) => <span className="tabular-nums text-xs">{row.activeCount ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const configMapColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'data', header: 'Data', accessor: (row) => <span className="tabular-nums text-xs">{row.dataCount ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const secretColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'type', header: 'Type', accessor: (row) => <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">{row.type || 'Opaque'}</span> },
  { key: 'data', header: 'Data', accessor: (row) => <span className="tabular-nums text-xs">{row.dataCount ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const hpaColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'target', header: 'Target', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.targetKind}/{row.targetName}</span> },
  { key: 'minmax', header: 'Min/Max', accessor: (row) => <span className="tabular-nums text-xs">{row.minReplicas ?? 0}/{row.maxReplicas ?? 0}</span>, align: 'center' },
  { key: 'replicas', header: 'Replicas', accessor: (row) => <span className="tabular-nums text-xs">{row.currentReplicas ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const resourceQuotaColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const limitRangeColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const pdbColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'minAvailable', header: 'Min Available', accessor: (row) => <span className="tabular-nums text-xs">{row.minAvailable || '-'}</span>, align: 'center' },
  { key: 'maxUnavailable', header: 'Max Unavailable', accessor: (row) => <span className="tabular-nums text-xs">{row.maxUnavailable || '-'}</span>, align: 'center' },
  { key: 'currentHealthy', header: 'Healthy', accessor: (row) => <span className="tabular-nums text-xs">{row.currentHealthy ?? 0}/{row.desiredHealthy ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const crdColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs truncate max-w-[300px] block">{row.name}</span> },
  { key: 'group', header: 'Group', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.group}</span> },
  { key: 'kind', header: 'Kind', accessor: (row) => <span className="text-xs text-muted-foreground">{row.kind}</span> },
  { key: 'version', header: 'Version', accessor: (row) => <span className="text-xs text-muted-foreground">{row.version}</span> },
  { key: 'scope', header: 'Scope', accessor: (row) => <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground">{row.scope}</span> },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const serviceAccountColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'secrets', header: 'Secrets', accessor: (row) => <span className="tabular-nums text-xs">{row.secretsCount ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const k8sRoleColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace || '-'}</span> },
  { key: 'rules', header: 'Rules', accessor: (row) => <span className="tabular-nums text-xs">{row.rulesCount ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const k8sRoleBindingColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace || '-'}</span> },
  { key: 'role', header: 'Role', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.roleKind}/{row.roleName}</span> },
  { key: 'subjects', header: 'Subjects', accessor: (row) => <span className="tabular-nums text-xs">{row.subjectsCount ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const endpointColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'endpoints', header: 'Endpoints', accessor: (row) => <span className="tabular-nums text-xs">{row.addressesCount ?? 0}</span>, align: 'center' },
  { key: 'ports', header: 'Ports', accessor: (row) => <span className="text-xs text-muted-foreground tabular-nums">{row.ports || '-'}</span>, sortable: false },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

const replicaSetColumns: Column<GenericK8sResource>[] = [
  { key: 'name', header: 'Name', accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span> },
  { key: 'namespace', header: 'Namespace', accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span> },
  { key: 'desired', header: 'Desired', accessor: (row) => <span className="tabular-nums text-xs">{row.desired ?? 0}</span>, align: 'center' },
  { key: 'ready', header: 'Ready', accessor: (row) => <span className="tabular-nums text-xs">{row.ready ?? 0}</span>, align: 'center' },
  { key: 'available', header: 'Available', accessor: (row) => <span className="tabular-nums text-xs">{row.available ?? 0}</span>, align: 'center' },
  { key: 'age', header: 'Age', accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span> },
];

// Map of generic resource type → columns
const genericColumnMap: Record<string, Column<GenericK8sResource>[]> = {
  jobs: jobColumns,
  cronjobs: cronJobColumns,
  configmaps: configMapColumns,
  secrets: secretColumns,
  hpa: hpaColumns,
  resourcequotas: resourceQuotaColumns,
  limitranges: limitRangeColumns,
  poddisruptionbudgets: pdbColumns,
  crds: crdColumns,
  serviceaccounts: serviceAccountColumns,
  'k8s-clusterroles': k8sRoleColumns,
  'k8s-clusterrolebindings': k8sRoleBindingColumns,
  'k8s-roles': k8sRoleColumns,
  'k8s-rolebindings': k8sRoleBindingColumns,
  endpoints: endpointColumns,
  replicasets: replicaSetColumns,
};

const WORKLOAD_KIND_TO_TEMPLATE: Record<string, string> = {
  Deployment: 'deployment',
  StatefulSet: 'statefulset',
  DaemonSet: 'daemonset',
};

type ResourcePermissionDecisions = {
  create: PermissionDecision;
  read: PermissionDecision;
  update: PermissionDecision;
  delete: PermissionDecision;
  scale: PermissionDecision;
  restart: PermissionDecision;
  exec: PermissionDecision;
  logs: PermissionDecision;
  manage: PermissionDecision;
};

function useClusterResourcePermissions(clusterId: string, resourceType: string): ResourcePermissionDecisions {
  const permissionResource = canonicalPermissionResource(resourceType);
  const scope = useMemo(() => ({ type: 'cluster' as const, id: clusterId }), [clusterId]);

  return {
    create: usePermissionDecision(permissionResource, 'create', scope),
    read: usePermissionDecision(permissionResource, 'read', scope),
    update: usePermissionDecision(permissionResource, 'update', scope),
    delete: usePermissionDecision(permissionResource, 'delete', scope),
    scale: usePermissionDecision(permissionResource, 'scale', scope),
    restart: usePermissionDecision(permissionResource, 'restart', scope),
    exec: usePermissionDecision(permissionResource, 'exec', scope),
    logs: usePermissionDecision(permissionResource, 'logs', scope),
    manage: usePermissionDecision(permissionResource, 'manage', scope),
  };
}

function permissionDeniedReason(decision: PermissionDecision): string {
  return decision.disabledReason || decision.reason;
}

function toastPermissionDenied(decision: PermissionDecision) {
  toastWarning(permissionDeniedReason(decision));
}

function firstDeniedDecision(...decisions: PermissionDecision[]): PermissionDecision | undefined {
  return decisions.find((decision) => !decision.allowed);
}

// ── Per-resource components (each calls only its own hook) ──

function NodesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterNodes(clusterId);
  const router = useRouter();
  const k8sPatch = useK8sPatch();
  const permissions = useClusterResourcePermissions(clusterId, 'nodes');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [drainTarget, setDrainTarget] = useState<ClusterNode | null>(null);

  const handleCordon = useCallback((node: ClusterNode) => {
    if (!permissions.update.allowed) {
      toastPermissionDenied(permissions.update);
      return;
    }
    k8sPatch.mutate({
      clusterId,
      path: k8sResourcePath('nodes', node.name),
      body: { spec: { unschedulable: true } },
      patchType: 'strategic-merge',
    });
  }, [clusterId, k8sPatch, permissions.update]);

  const handleUncordon = useCallback((node: ClusterNode) => {
    if (!permissions.update.allowed) {
      toastPermissionDenied(permissions.update);
      return;
    }
    k8sPatch.mutate({
      clusterId,
      path: k8sResourcePath('nodes', node.name),
      body: { spec: { unschedulable: false } },
      patchType: 'strategic-merge',
    });
  }, [clusterId, k8sPatch, permissions.update]);

  const handleDrain = async (node: ClusterNode) => {
    if (!permissions.manage.allowed) {
      toastPermissionDenied(permissions.manage);
      return;
    }
    try {
      const result = await apiClient.drainNode(clusterId, node.name, {
        ignore_daemonsets: true,
        delete_empty_dir_data: false,
      });
      if (result.status === 'blocked') {
        toastWarning(result.message);
      } else if (result.status === 'partial') {
        toastWarning(result.message);
      } else {
        toastSuccess(result.message || `Node ${node.name} drained`);
      }
      setDrainTarget(null);
    } catch (error) {
      toastApiError('Failed to drain node', error);
    }
  };

  const columns = useMemo<Column<ClusterNode>[]>(() => [
    ...nodeColumns,
    {
      key: 'actions',
      header: '',
      accessor: (row) => {
        const isCordonable = row.status !== 'SchedulingDisabled';
        return (
          <ActionMenu
            items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('nodes', row.name), title: `Node: ${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: isCordonable ? 'Cordon' : 'Uncordon',
                icon: isCordonable ? <ShieldBan className="h-3.5 w-3.5" /> : <ShieldCheck className="h-3.5 w-3.5" />,
                onClick: () => isCordonable ? handleCordon(row) : handleUncordon(row),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
                separator: true,
              },
              {
                label: 'Drain',
                icon: <Unplug className="h-3.5 w-3.5" />,
                onClick: () => setDrainTarget(row),
                variant: 'destructive',
                disabled: !permissions.manage.allowed,
                disabledReason: permissionDeniedReason(permissions.manage),
              },
            ]}
          />
        );
      },
      sortable: false,
      align: 'center' as const,
    },
  ], [handleCordon, handleUncordon, permissions.manage, permissions.read, permissions.update]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => r.name}
        searchPlaceholder="Search nodes..." loading={isLoading} emptyMessage="No nodes found"
        onRowClick={(row) => {
          if (!permissions.read.allowed) {
            toastPermissionDenied(permissions.read);
            return;
          }
          router.push(`/dashboard/clusters/${clusterId}/nodes/${row.name}`);
        }} />

      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={permissions.update.allowed}
        />
      )}

      <ConfirmDialog
        open={!!drainTarget}
        onClose={() => setDrainTarget(null)}
        onConfirm={() => { if (drainTarget) void handleDrain(drainTarget); }}
        title="Drain Node"
        description={`This will cordon the node and evict all non-DaemonSet pods. Workloads will be rescheduled to other nodes.`}
        confirmValue={drainTarget?.name}
        confirmText="Drain"
        variant="destructive"
      />
    </>
  );
}

function NamespacesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterNamespaces(clusterId);
  const router = useRouter();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, 'namespaces');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Namespace | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const [newNsName, setNewNsName] = useState('');
  const k8sCreate = apiClient.k8sCreate;

  const handleCreateNamespace = async () => {
    if (!permissions.update.allowed) {
      toastPermissionDenied(permissions.update);
      return;
    }
    if (!newNsName.trim()) return;
    try {
      await k8sCreate(clusterId, 'api/v1/namespaces', {
        apiVersion: 'v1',
        kind: 'Namespace',
        metadata: { name: newNsName.trim() },
      });
      toastSuccess(`Namespace ${newNsName} created`);
      setShowCreate(false);
      setNewNsName('');
    } catch (error) {
      toastApiError('Failed to create namespace', error);
    }
  };

  const columns = useMemo<Column<Namespace>[]>(() => [
    nameColumn<Namespace>(clusterId, 'namespaces'),
    ...nsColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('namespaces', row.name), title: `Namespace: ${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('namespaces', row.name), title: `Namespace: ${row.name}` }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
                separator: true,
              },
          ]}
        /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.read, permissions.update]);

  return (
    <>
      <div className="flex items-center justify-between mb-4">
        <div />
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.update.allowed}
          disabledReason={permissionDeniedReason(permissions.update)}
        >
          Create Namespace
        </ActionButton>
      </div>

      <DataTable data={data || []} columns={columns} keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(router, clusterId, 'namespaces', permissions.read)}
        searchPlaceholder="Search namespaces..." loading={isLoading} emptyMessage="No namespaces found" />

      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={permissions.update.allowed}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.update.allowed) {
            toastPermissionDenied(permissions.update);
            return;
          }
          if (deleteTarget) {
            k8sDeleteMut.mutate(
              { clusterId, path: k8sResourcePath('namespaces', deleteTarget.name) },
              { onSuccess: () => setDeleteTarget(null) }
            );
          }
        }}
        title="Delete Namespace"
        description="This will delete the namespace and ALL resources within it. This action cannot be undone."
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />

      {/* Create Namespace Dialog */}
      {showCreate && (
        <OverlayShell onClose={() => setShowCreate(false)}>
          <div className="relative bg-card border border-border rounded-lg shadow-xl max-w-md w-full mx-4 animate-fade-in p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">Create Namespace</h3>
            <label className="block text-xs text-muted-foreground mb-1.5">Name</label>
            <input
              type="text"
              value={newNsName}
              onChange={(e) => setNewNsName(e.target.value)}
              placeholder="my-namespace"
              className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-1 focus:ring-ring"
              autoFocus
              onKeyDown={(e) => { if (e.key === 'Enter') handleCreateNamespace(); }}
            />
            <div className="flex items-center justify-end gap-2 mt-4">
              <button onClick={() => setShowCreate(false)}
                className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
                Cancel
              </button>
              <button
                onClick={handleCreateNamespace}
                disabled={!newNsName.trim() || !permissions.update.allowed}
                title={!permissions.update.allowed ? permissionDeniedReason(permissions.update) : undefined}
                className="h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90
                  disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                Create
              </button>
            </div>
          </div>
        </OverlayShell>
      )}
    </>
  );
}

function EventsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useClusterEvents(clusterId, { limit: 200 });
  return (
    <DataTable data={data || []} columns={eventColumns} keyExtractor={(r) => r.id}
      searchPlaceholder="Search events..." loading={isLoading} emptyMessage="No events found" />
  );
}

function PodsTable({ clusterId }: { clusterId: string }) {
  const router = useRouter();
  const { data, isLoading } = useClusterPods(clusterId);
  const deletePod = useDeletePod();
  const permissions = useClusterResourcePermissions(clusterId, 'pods');

  const [deleteTarget, setDeleteTarget] = useState<Pod | null>(null);
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);

  // Open a pod-logs / exec session inside the global WindowManager drawer.
  // The drawer mounts at the dashboard layout level and persists across
  // navigation, so we do NOT manage its open/close lifecycle here.
  const openLogs = useCallback((pod: Pod) => {
    if (!permissions.logs.allowed) {
      toastPermissionDenied(permissions.logs);
      return;
    }
    useWindowManagerStore.getState().addTab({
      kind: 'logs',
      clusterId,
      namespace: pod.namespace,
      pod: pod.name,
      container: pod.containers[0]?.name,
    });
  }, [clusterId, permissions.logs]);
  const openExec = useCallback((pod: Pod) => {
    if (!permissions.exec.allowed) {
      toastPermissionDenied(permissions.exec);
      return;
    }
    useWindowManagerStore.getState().addTab({
      kind: 'exec',
      clusterId,
      namespace: pod.namespace,
      pod: pod.name,
      container: pod.containers[0]?.name,
    });
  }, [clusterId, permissions.exec]);

  const columns = useMemo<Column<Pod>[]>(() => [
    // Override the shared name cell with a drill-down link into pod detail.
    nameColumn<Pod>(clusterId, 'pods'),
    ...podColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick>
        <ActionMenu
          items={[
            {
              label: 'Execute Shell',
              icon: <Terminal className="h-3.5 w-3.5" />,
              onClick: () => openExec(row),
              disabled: row.phase !== 'Running' || !permissions.exec.allowed,
              disabledReason: row.phase !== 'Running' ? 'Pod must be running.' : permissionDeniedReason(permissions.exec),
            },
            {
              label: 'View Logs',
              icon: <FileText className="h-3.5 w-3.5" />,
              onClick: () => openLogs(row),
              disabled: !permissions.logs.allowed,
              disabledReason: permissionDeniedReason(permissions.logs),
            },
            {
              label: 'View YAML',
              icon: <Code className="h-3.5 w-3.5" />,
              onClick: () => setYamlTarget({
                path: k8sResourcePath('pods', row.name, row.namespace),
                title: `Pod: ${row.namespace}/${row.name}`,
              }),
              disabled: !permissions.read.allowed,
              disabledReason: permissionDeniedReason(permissions.read),
              separator: true,
            },
            {
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: 'destructive',
              disabled: !permissions.delete.allowed,
              disabledReason: permissionDeniedReason(permissions.delete),
              separator: true,
            },
          ]}
        />
        </StopRowClick>
      ),
      sortable: false,
      align: 'center',
    },
  ], [clusterId, openExec, openLogs, permissions.delete, permissions.exec, permissions.logs, permissions.read]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        searchPlaceholder="Search pods..." loading={isLoading} emptyMessage="No pods found"
        onRowClick={makeRowClick(router, clusterId, 'pods', permissions.read)} />

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) {
            deletePod.mutate(
              { clusterId, namespace: deleteTarget.namespace, name: deleteTarget.name },
              { onSuccess: () => setDeleteTarget(null) }
            );
          }
        }}
        title="Delete Pod"
        description={`This will permanently delete the pod. The owning controller may recreate it.`}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={deletePod.isPending}
      />

      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={permissions.update.allowed}
        />
      )}
    </>
  );
}

function WorkloadsTable({ clusterId, kind, title }: { clusterId: string; kind: string; title: string }) {
  const { data, isLoading } = useWorkloads(clusterId);
  const router = useRouter();
  const filtered = (data?.data || []).filter((w) => w.kind === kind);
  const scaleWorkload = useScaleWorkload();
  const restartWorkload = useRestartWorkload();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, 'workloads');
  const podPermissions = useClusterResourcePermissions(clusterId, 'pods');

  const [scaleTarget, setScaleTarget] = useState<Workload | null>(null);
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Workload | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  // Resolve the running pod for a workload then push a tab into the global
  // WindowManager. We deliberately don't open a per-page panel: the drawer
  // lives at the dashboard layout level and survives navigation.
  const openWorkloadInWindowManager = useCallback(async (
    workload: Workload,
    kind: 'logs' | 'exec'
  ) => {
    const denied = kind === 'exec'
      ? firstDeniedDecision(podPermissions.read, podPermissions.exec)
      : firstDeniedDecision(podPermissions.read, podPermissions.logs);
    if (denied) {
      toastPermissionDenied(denied);
      return;
    }
    try {
      const pods = await apiClient.getWorkloadPods(
        clusterId,
        workload.kind,
        workload.namespace,
        workload.name,
      );
      const runningPod = pods.find((p) => p.phase === 'Running') || pods[0];
      if (!runningPod) {
        toastError('No pods available for this workload');
        return;
      }
      useWindowManagerStore.getState().addTab({
        kind,
        clusterId,
        namespace: runningPod.namespace,
        pod: runningPod.name,
        container: runningPod.containers[0]?.name,
      });
    } catch {
      toastError('Failed to fetch workload pods');
    }
  }, [clusterId, podPermissions.exec, podPermissions.logs, podPermissions.read]);

  const columns = useMemo<Column<Workload>[]>(() => [
    // Override the shared workloadColumns "name" cell with a Link into the
    // per-cluster workload detail route so users can drill in from the list.
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <Link
          href={workloadDetailHref(clusterId, row.kind, row.namespace, row.name)}
          onClick={(e) => e.stopPropagation()}
          className="font-medium text-foreground font-mono text-xs hover:underline"
        >
          {row.name}
        </Link>
      ),
    },
    ...workloadColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => {
        const resType = kindToResourceType(row.kind);
        const execDenied = firstDeniedDecision(podPermissions.read, podPermissions.exec);
        const logsDenied = firstDeniedDecision(podPermissions.read, podPermissions.logs);
        const items: ActionMenuItem[] = [
          {
            label: 'Execute Shell',
            icon: <Terminal className="h-3.5 w-3.5" />,
            onClick: () => openWorkloadInWindowManager(row, 'exec'),
            disabled: Boolean(execDenied),
            disabledReason: execDenied ? permissionDeniedReason(execDenied) : undefined,
          },
          {
            label: 'View Logs',
            icon: <FileText className="h-3.5 w-3.5" />,
            onClick: () => openWorkloadInWindowManager(row, 'logs'),
            disabled: Boolean(logsDenied),
            disabledReason: logsDenied ? permissionDeniedReason(logsDenied) : undefined,
          },
          {
            label: 'View YAML',
            icon: <Code className="h-3.5 w-3.5" />,
            onClick: () => setYamlTarget({
              path: k8sResourcePath(resType, row.name, row.namespace),
              title: `${row.kind}: ${row.namespace}/${row.name}`,
            }),
            disabled: !permissions.read.allowed,
            disabledReason: permissionDeniedReason(permissions.read),
            separator: true,
          },
          {
            label: 'Edit YAML',
            icon: <Pencil className="h-3.5 w-3.5" />,
            onClick: () => setYamlTarget({
              path: k8sResourcePath(resType, row.name, row.namespace),
              title: `${row.kind}: ${row.namespace}/${row.name}`,
            }),
            disabled: !permissions.update.allowed,
            disabledReason: permissionDeniedReason(permissions.update),
          },
        ];
        if (WORKLOAD_SCALABLE_KINDS.includes(row.kind)) {
          items.push({
            label: 'Scale',
            icon: <Scaling className="h-3.5 w-3.5" />,
            onClick: () => setScaleTarget(row),
            disabled: !permissions.scale.allowed,
            disabledReason: permissionDeniedReason(permissions.scale),
            separator: true,
          });
        }
        items.push({
          label: 'Restart',
          icon: <RotateCw className="h-3.5 w-3.5" />,
          onClick: () => {
            if (!permissions.restart.allowed) {
              toastPermissionDenied(permissions.restart);
              return;
            }
            restartWorkload.mutate({
              clusterId,
              kind: row.kind,
              namespace: row.namespace,
              name: row.name,
            });
          },
          disabled: !permissions.restart.allowed,
          disabledReason: permissionDeniedReason(permissions.restart),
          separator: !WORKLOAD_SCALABLE_KINDS.includes(row.kind),
        });
        items.push({
          label: 'Delete',
          icon: <Trash2 className="h-3.5 w-3.5" />,
          onClick: () => setDeleteTarget(row),
          variant: 'destructive',
          disabled: !permissions.delete.allowed,
          disabledReason: permissionDeniedReason(permissions.delete),
          separator: true,
        });
        return <StopRowClick><ActionMenu items={items} /></StopRowClick>;
      },
      sortable: false,
      align: 'center',
    },
  ], [
    clusterId,
    openWorkloadInWindowManager,
    permissions.delete,
    permissions.read,
    permissions.restart,
    permissions.scale,
    permissions.update,
    podPermissions.exec,
    podPermissions.logs,
    podPermissions.read,
    restartWorkload,
  ]);

  return (
    <>
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create {kind}
        </ActionButton>
      </div>
      <DataTable data={filtered} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={(row) => {
          if (!permissions.read.allowed) {
            toastPermissionDenied(permissions.read);
            return;
          }
          router.push(workloadDetailHref(clusterId, row.kind, row.namespace, row.name));
        }}
        searchPlaceholder={`Search ${title.toLowerCase()}...`} loading={isLoading} emptyMessage={`No ${title.toLowerCase()} found`} />

      <ScaleDialog
        open={!!scaleTarget}
        onClose={() => setScaleTarget(null)}
        onScale={(replicas) => {
          if (!permissions.scale.allowed) {
            toastPermissionDenied(permissions.scale);
            return;
          }
          if (scaleTarget) {
            scaleWorkload.mutate(
              {
                clusterId,
                kind: scaleTarget.kind,
                namespace: scaleTarget.namespace,
                name: scaleTarget.name,
                replicas,
              },
              { onSuccess: () => setScaleTarget(null) }
            );
          }
        }}
        workloadName={scaleTarget?.name || ''}
        currentReplicas={scaleTarget?.replicas || 0}
        loading={scaleWorkload.isPending}
      />

      {yamlTarget && (
        <YamlViewDialog
          open={!!yamlTarget}
          onClose={() => setYamlTarget(null)}
          clusterId={clusterId}
          k8sPath={yamlTarget.path}
          title={yamlTarget.title}
          allowEdit={permissions.update.allowed}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) {
            const resType = kindToResourceType(deleteTarget.kind);
            k8sDeleteMut.mutate(
              { clusterId, path: k8sResourcePath(resType, deleteTarget.name, deleteTarget.namespace) },
              { onSuccess: () => setDeleteTarget(null) }
            );
          }
        }}
        title={`Delete ${deleteTarget?.kind || 'Workload'}`}
        description={`This will permanently delete ${deleteTarget?.name}. Managed pods will also be terminated.`}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />

      {WORKLOAD_KIND_TO_TEMPLATE[kind] && (
        <CreateResourceDialog
          open={showCreate}
          onClose={() => setShowCreate(false)}
          clusterId={clusterId}
          templateKey={WORKLOAD_KIND_TO_TEMPLATE[kind]}
          title={`Create ${kind}`}
        />
      )}
    </>
  );
}

function ServicesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useServices(clusterId);
  const router = useRouter();
  const deleteService = useDeleteService();
  const permissions = useClusterResourcePermissions(clusterId, 'services');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<K8sService | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<K8sService>[]>(() => [
    nameColumn<K8sService>(clusterId, 'services'),
    ...serviceColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('services', row.name, row.namespace), title: `Service: ${row.namespace}/${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('services', row.name, row.namespace), title: `Service: ${row.namespace}/${row.name}` }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.delete.allowed,
                disabledReason: permissionDeniedReason(permissions.delete),
                separator: true,
              },
          ]}
        /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.delete, permissions.read, permissions.update]);

  return (
    <>
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Service
        </ActionButton>
      </div>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, 'services', permissions.read)}
        searchPlaceholder="Search services..." loading={isLoading} emptyMessage="No services found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) deleteService.mutate({ clusterId, namespace: deleteTarget.namespace, name: deleteTarget.name }, { onSuccess: () => setDeleteTarget(null) });
        }}
        title="Delete Service" description={`This will permanently delete the service ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={deleteService.isPending}
      />
      <CreateResourceDialog open={showCreate} onClose={() => setShowCreate(false)} clusterId={clusterId}
        templateKey="service" title="Create Service" />
    </>
  );
}

function IngressesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useIngresses(clusterId);
  const router = useRouter();
  const deleteIngress = useDeleteIngress();
  const permissions = useClusterResourcePermissions(clusterId, 'ingresses');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Ingress | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<Ingress>[]>(() => [
    nameColumn<Ingress>(clusterId, 'ingresses'),
    ...ingressColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('ingresses', row.name, row.namespace), title: `Ingress: ${row.namespace}/${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('ingresses', row.name, row.namespace), title: `Ingress: ${row.namespace}/${row.name}` }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.delete.allowed,
                disabledReason: permissionDeniedReason(permissions.delete),
                separator: true,
              },
          ]}
        /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.delete, permissions.read, permissions.update]);

  return (
    <>
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Ingress
        </ActionButton>
      </div>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, 'ingresses', permissions.read)}
        searchPlaceholder="Search ingresses..." loading={isLoading} emptyMessage="No ingresses found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) deleteIngress.mutate({ clusterId, namespace: deleteTarget.namespace, name: deleteTarget.name }, { onSuccess: () => setDeleteTarget(null) });
        }}
        title="Delete Ingress" description={`This will permanently delete the ingress ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={deleteIngress.isPending}
      />
      <CreateResourceDialog open={showCreate} onClose={() => setShowCreate(false)} clusterId={clusterId}
        templateKey="ingress" title="Create Ingress" />
    </>
  );
}

function NetworkPoliciesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useNetworkPolicies(clusterId);
  const router = useRouter();
  const deleteNp = useDeleteNetworkPolicy();
  const permissions = useClusterResourcePermissions(clusterId, 'networkpolicies');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<NetworkPolicy | null>(null);
  const [showCreate, setShowCreate] = useState(false);

  const columns = useMemo<Column<NetworkPolicy>[]>(() => [
    nameColumn<NetworkPolicy>(clusterId, 'networkpolicies'),
    ...networkPolicyColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('networkpolicies', row.name, row.namespace), title: `NetworkPolicy: ${row.namespace}/${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('networkpolicies', row.name, row.namespace), title: `NetworkPolicy: ${row.namespace}/${row.name}` }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.delete.allowed,
                disabledReason: permissionDeniedReason(permissions.delete),
                separator: true,
              },
          ]}
        /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.delete, permissions.read, permissions.update]);

  return (
    <>
      <div className="flex justify-end mb-4">
        <ActionButton
          size="sm"
          intent="primary"
          icon={<Plus className="h-3.5 w-3.5" />}
          onClick={() => setShowCreate(true)}
          disabled={!permissions.create.allowed}
          disabledReason={permissionDeniedReason(permissions.create)}
        >
          Create Network Policy
        </ActionButton>
      </div>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, 'networkpolicies', permissions.read)}
        searchPlaceholder="Search network policies..." loading={isLoading} emptyMessage="No network policies found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) deleteNp.mutate({ clusterId, namespace: deleteTarget.namespace, name: deleteTarget.name }, { onSuccess: () => setDeleteTarget(null) });
        }}
        title="Delete Network Policy" description={`This will permanently delete the network policy ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={deleteNp.isPending}
      />
      <CreateResourceDialog open={showCreate} onClose={() => setShowCreate(false)} clusterId={clusterId}
        templateKey="networkpolicy" title="Create Network Policy" />
    </>
  );
}

// ── Gateway API ─────────────────────────────────────────────────────────────
//
// Read + YAML edit + delete. No bespoke create forms — users author YAML via
// the standard CreateResourceDialog only for the four resource types that
// have templates today (service / ingress / networkpolicy / pvc).

// Renders the True/False/Unknown values that Kubernetes publishes for
// gateway-api status conditions. Empty string means the controller hasn't
// emitted the condition yet.
function ConditionPill({ status, trueLabel, falseLabel }: { status: string; trueLabel: string; falseLabel: string }) {
  if (!status) return <span className="text-xs text-muted-foreground">—</span>;
  if (status === 'True') {
    return <span className="text-xs px-1.5 py-0.5 rounded bg-status-success/10 text-status-success">{trueLabel}</span>;
  }
  if (status === 'False') {
    return <span className="text-xs px-1.5 py-0.5 rounded bg-status-danger/10 text-status-danger">{falseLabel}</span>;
  }
  return <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">{status}</span>;
}

const gatewayColumns: Column<Gateway>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'class',
    header: 'Class',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.gatewayClassName || '-'}</span>,
  },
  {
    key: 'listeners',
    header: 'Listeners',
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.listenerSummary?.length
          ? row.listenerSummary.map((s, i) => (
              <span key={`${s}-${i}`} className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono">{s}</span>
            ))
          : <span className="text-xs text-muted-foreground">-</span>}
      </div>
    ),
    sortable: false,
  },
  {
    key: 'addresses',
    header: 'Addresses',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
        {row.addresses?.join(', ') || '-'}
      </span>
    ),
    sortable: false,
  },
  {
    key: 'programmed',
    header: 'Programmed',
    accessor: (row) => <ConditionPill status={row.programmed} trueLabel="Programmed" falseLabel="Failed" />,
    align: 'center',
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

// Shared column definition for HTTPRoute / GRPCRoute / TLSRoute / TCPRoute /
// UDPRoute. The L4 routes (TCP/UDP) won't populate hostnames; their column
// just renders empty.
const routeColumns: Column<GatewayRoute>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'parents',
    header: 'Parent Gateways',
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.parentSummary?.length
          ? row.parentSummary.map((p, i) => (
              <span key={`${p}-${i}`} className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono">{p}</span>
            ))
          : <span className="text-xs text-muted-foreground">-</span>}
      </div>
    ),
    sortable: false,
  },
  {
    key: 'hostnames',
    header: 'Hostnames',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[200px] block">
        {row.hostnames?.join(', ') || '-'}
      </span>
    ),
    sortable: false,
  },
  {
    key: 'rules',
    header: 'Rules',
    accessor: (row) => <span className="tabular-nums text-xs">{row.ruleCount}</span>,
    align: 'center',
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const gatewayClassColumns: Column<GatewayClass>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'controllerName',
    header: 'Controller',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono truncate max-w-[280px] block">{row.controllerName}</span>,
  },
  {
    key: 'accepted',
    header: 'Accepted',
    accessor: (row) => <ConditionPill status={row.accepted} trueLabel="Accepted" falseLabel="Rejected" />,
    align: 'center',
  },
  {
    key: 'description',
    header: 'Description',
    accessor: (row) => <span className="text-xs text-muted-foreground truncate max-w-[260px] block">{row.description || '-'}</span>,
    sortable: false,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

const referenceGrantColumns: Column<ReferenceGrant>[] = [
  {
    key: 'name',
    header: 'Name',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>,
  },
  {
    key: 'namespace',
    header: 'Namespace',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.namespace}</span>,
  },
  {
    key: 'from',
    header: 'From',
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.from?.length
          ? row.from.map((f, i) => (
              <span key={`${f.kind}-${f.namespace}-${i}`} className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono">
                {f.kind}@{f.namespace}
              </span>
            ))
          : <span className="text-xs text-muted-foreground">-</span>}
      </div>
    ),
    sortable: false,
  },
  {
    key: 'to',
    header: 'To',
    accessor: (row) => (
      <div className="flex gap-1 flex-wrap">
        {row.to?.length
          ? row.to.map((t, i) => (
              <span key={`${t.kind}-${t.name}-${i}`} className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground font-mono">
                {t.kind}{t.name ? `/${t.name}` : ''}
              </span>
            ))
          : <span className="text-xs text-muted-foreground">-</span>}
      </div>
    ),
    sortable: false,
  },
  {
    key: 'age',
    header: 'Age',
    accessor: (row) => <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>,
  },
];

// useK8sDelete provides the mutation; per-row dialog state lives in each
// table. Shared utility to render the action menu for a namespaced row.
function NamespacedActions<T extends { name: string; namespace: string }>({
  resourceType,
  kindLabel,
  row,
  permissions,
  onView,
  onDelete,
}: {
  resourceType: string;
  kindLabel: string;
  row: T;
  permissions: ResourcePermissionDecisions;
  onView: (target: { path: string; title: string }) => void;
  onDelete: (row: T) => void;
}) {
  const path = k8sResourcePath(resourceType, row.name, row.namespace);
  const title = `${kindLabel}: ${row.namespace}/${row.name}`;
  return (
    <StopRowClick>
      <ActionMenu
        items={[
          {
            label: 'View YAML',
            icon: <Code className="h-3.5 w-3.5" />,
            onClick: () => onView({ path, title }),
            disabled: !permissions.read.allowed,
            disabledReason: permissionDeniedReason(permissions.read),
          },
          {
            label: 'Edit YAML',
            icon: <Pencil className="h-3.5 w-3.5" />,
            onClick: () => onView({ path, title }),
            disabled: !permissions.update.allowed,
            disabledReason: permissionDeniedReason(permissions.update),
          },
          {
            label: 'Delete',
            icon: <Trash2 className="h-3.5 w-3.5" />,
            onClick: () => onDelete(row),
            variant: 'destructive',
            disabled: !permissions.delete.allowed,
            disabledReason: permissionDeniedReason(permissions.delete),
            separator: true,
          },
        ]}
      />
    </StopRowClick>
  );
}

function GatewaysTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useGateways(clusterId);
  const router = useRouter();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, 'gateways');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Gateway | null>(null);

  const columns = useMemo<Column<Gateway>[]>(() => [
    nameColumn<Gateway>(clusterId, 'gateways'),
    ...gatewayColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <NamespacedActions resourceType="gateways" kindLabel="Gateway" row={row} permissions={permissions} onView={setYamlTarget} onDelete={setDeleteTarget} />
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, 'gateways', permissions.read)}
        searchPlaceholder="Search gateways..." loading={isLoading} emptyMessage="No gateways found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) k8sDelete.mutate(
            { clusterId, path: k8sResourcePath('gateways', deleteTarget.name, deleteTarget.namespace) },
            { onSuccess: () => setDeleteTarget(null) },
          );
        }}
        title="Delete Gateway" description={`This will permanently delete the gateway ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={k8sDelete.isPending}
      />
    </>
  );
}

// Route tables share routeColumns + a delete confirm. The variants only
// differ in which hook supplies data and which resourceType the K8s path
// builder gets. Parameterizing keeps drift between them minimal.
function RouteTable<T extends GatewayRoute>({
  clusterId, kindLabel, resourceType, data, isLoading, searchPlaceholder, emptyMessage,
}: {
  clusterId: string;
  kindLabel: string;
  resourceType: string;
  data: T[] | undefined;
  isLoading: boolean;
  searchPlaceholder: string;
  emptyMessage: string;
}) {
  const router = useRouter();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, resourceType);
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<T | null>(null);

  const columns = useMemo<Column<T>[]>(() => [
    nameColumn<T>(clusterId, resourceType),
    ...(routeColumns.slice(1) as Column<T>[]),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <NamespacedActions resourceType={resourceType} kindLabel={kindLabel} row={row} permissions={permissions} onView={setYamlTarget} onDelete={(r) => setDeleteTarget(r as T)} />
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, resourceType, kindLabel, permissions]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, resourceType, permissions.read)}
        searchPlaceholder={searchPlaceholder} loading={isLoading} emptyMessage={emptyMessage} />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) k8sDelete.mutate(
            { clusterId, path: k8sResourcePath(resourceType, deleteTarget.name, deleteTarget.namespace) },
            { onSuccess: () => setDeleteTarget(null) },
          );
        }}
        title={`Delete ${kindLabel}`} description={`This will permanently delete the ${kindLabel.toLowerCase()} ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={k8sDelete.isPending}
      />
    </>
  );
}

function HTTPRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useHTTPRoutes(clusterId);
  return <RouteTable clusterId={clusterId} kindLabel="HTTPRoute" resourceType="httproutes"
    data={data} isLoading={isLoading} searchPlaceholder="Search HTTPRoutes..." emptyMessage="No HTTPRoutes found" />;
}

function GRPCRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useGRPCRoutes(clusterId);
  return <RouteTable clusterId={clusterId} kindLabel="GRPCRoute" resourceType="grpcroutes"
    data={data} isLoading={isLoading} searchPlaceholder="Search GRPCRoutes..." emptyMessage="No GRPCRoutes found" />;
}

function TLSRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useTLSRoutes(clusterId);
  return <RouteTable clusterId={clusterId} kindLabel="TLSRoute" resourceType="tlsroutes"
    data={data} isLoading={isLoading} searchPlaceholder="Search TLSRoutes..." emptyMessage="No TLSRoutes found" />;
}

function TCPRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useTCPRoutes(clusterId);
  return <RouteTable clusterId={clusterId} kindLabel="TCPRoute" resourceType="tcproutes"
    data={data} isLoading={isLoading} searchPlaceholder="Search TCPRoutes..." emptyMessage="No TCPRoutes found" />;
}

function UDPRoutesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useUDPRoutes(clusterId);
  return <RouteTable clusterId={clusterId} kindLabel="UDPRoute" resourceType="udproutes"
    data={data} isLoading={isLoading} searchPlaceholder="Search UDPRoutes..." emptyMessage="No UDPRoutes found" />;
}

function GatewayClassesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useGatewayClasses(clusterId);
  const router = useRouter();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, 'gatewayclasses');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<GatewayClass | null>(null);

  const columns = useMemo<Column<GatewayClass>[]>(() => [
    nameColumn<GatewayClass>(clusterId, 'gatewayclasses'),
    ...gatewayClassColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => {
        const path = k8sResourcePath('gatewayclasses', row.name);
        const title = `GatewayClass: ${row.name}`;
        return (
          <StopRowClick><ActionMenu
            items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path, title }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path, title }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.delete.allowed,
                disabledReason: permissionDeniedReason(permissions.delete),
                separator: true,
              },
            ]}
          /></StopRowClick>
        );
      },
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.delete, permissions.read, permissions.update]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(router, clusterId, 'gatewayclasses', permissions.read)}
        searchPlaceholder="Search GatewayClasses..." loading={isLoading} emptyMessage="No GatewayClasses found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) k8sDelete.mutate(
            { clusterId, path: k8sResourcePath('gatewayclasses', deleteTarget.name) },
            { onSuccess: () => setDeleteTarget(null) },
          );
        }}
        title="Delete GatewayClass" description={`This will permanently delete the gateway class ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={k8sDelete.isPending}
      />
    </>
  );
}

function ReferenceGrantsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useReferenceGrants(clusterId);
  const router = useRouter();
  const k8sDelete = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, 'referencegrants');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ReferenceGrant | null>(null);

  const columns = useMemo<Column<ReferenceGrant>[]>(() => [
    nameColumn<ReferenceGrant>(clusterId, 'referencegrants'),
    ...referenceGrantColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <NamespacedActions resourceType="referencegrants" kindLabel="ReferenceGrant" row={row} permissions={permissions} onView={setYamlTarget} onDelete={setDeleteTarget} />
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, 'referencegrants', permissions.read)}
        searchPlaceholder="Search ReferenceGrants..." loading={isLoading} emptyMessage="No ReferenceGrants found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) k8sDelete.mutate(
            { clusterId, path: k8sResourcePath('referencegrants', deleteTarget.name, deleteTarget.namespace) },
            { onSuccess: () => setDeleteTarget(null) },
          );
        }}
        title="Delete ReferenceGrant" description={`This will permanently delete the reference grant ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={k8sDelete.isPending}
      />
    </>
  );
}

function PVsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = usePersistentVolumes(clusterId);
  const router = useRouter();
  const deletePv = useDeletePV();
  const permissions = useClusterResourcePermissions(clusterId, 'persistentvolumes');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<PersistentVolume | null>(null);

  const columns = useMemo<Column<PersistentVolume>[]>(() => [
    nameColumn<PersistentVolume>(clusterId, 'persistentvolumes'),
    ...pvColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('persistentvolumes', row.name), title: `PV: ${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('persistentvolumes', row.name), title: `PV: ${row.name}` }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.delete.allowed,
                disabledReason: permissionDeniedReason(permissions.delete),
                separator: true,
              },
          ]}
        /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.delete, permissions.read, permissions.update]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(router, clusterId, 'persistentvolumes', permissions.read)}
        searchPlaceholder="Search persistent volumes..." loading={isLoading} emptyMessage="No persistent volumes found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) deletePv.mutate({ clusterId, name: deleteTarget.name }, { onSuccess: () => setDeleteTarget(null) });
        }}
        title="Delete Persistent Volume" description={`This will permanently delete the PV ${deleteTarget?.name}. Bound data may be lost.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={deletePv.isPending}
      />
    </>
  );
}

function PVCsTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = usePersistentVolumeClaims(clusterId);
  const router = useRouter();
  const deletePvc = useDeletePVC();
  const permissions = useClusterResourcePermissions(clusterId, 'persistentvolumeclaims');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<PersistentVolumeClaim | null>(null);

  const columns = useMemo<Column<PersistentVolumeClaim>[]>(() => [
    nameColumn<PersistentVolumeClaim>(clusterId, 'persistentvolumeclaims'),
    ...pvcColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('persistentvolumeclaims', row.name, row.namespace), title: `PVC: ${row.namespace}/${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
              {
                label: 'Edit YAML',
                icon: <Pencil className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('persistentvolumeclaims', row.name, row.namespace), title: `PVC: ${row.namespace}/${row.name}` }),
                disabled: !permissions.update.allowed,
                disabledReason: permissionDeniedReason(permissions.update),
              },
              {
                label: 'Delete',
                icon: <Trash2 className="h-3.5 w-3.5" />,
                onClick: () => setDeleteTarget(row),
                variant: 'destructive',
                disabled: !permissions.delete.allowed,
                disabledReason: permissionDeniedReason(permissions.delete),
                separator: true,
              },
          ]}
        /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.delete, permissions.read, permissions.update]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => `${r.namespace}/${r.name}`}
        onRowClick={makeRowClick(router, clusterId, 'persistentvolumeclaims', permissions.read)}
        searchPlaceholder="Search PVCs..." loading={isLoading} emptyMessage="No persistent volume claims found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget) deletePvc.mutate({ clusterId, namespace: deleteTarget.namespace, name: deleteTarget.name }, { onSuccess: () => setDeleteTarget(null) });
        }}
        title="Delete PVC" description={`This will permanently delete the PVC ${deleteTarget?.name}. Bound data may be lost.`}
        confirmValue={deleteTarget?.name} variant="destructive" loading={deletePvc.isPending}
      />
    </>
  );
}

function StorageClassesTable({ clusterId }: { clusterId: string }) {
  const { data, isLoading } = useStorageClasses(clusterId);
  const router = useRouter();
  const permissions = useClusterResourcePermissions(clusterId, 'storageclasses');
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);

  const columns = useMemo<Column<StorageClass>[]>(() => [
    // SC name keeps its "default" badge alongside the drill-down link.
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <NameLink clusterId={clusterId} resourceType="storageclasses" name={row.name} />
          {row.isDefault && (
            <span className="px-1.5 py-0.5 rounded text-2xs bg-status-info/10 text-status-info">default</span>
          )}
        </div>
      ),
      sortAccessor: (row) => row.name,
    },
    ...storageClassColumns.slice(1),
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <StopRowClick><ActionMenu
          items={[
              {
                label: 'View YAML',
                icon: <Code className="h-3.5 w-3.5" />,
                onClick: () => setYamlTarget({ path: k8sResourcePath('storageclasses', row.name), title: `StorageClass: ${row.name}` }),
                disabled: !permissions.read.allowed,
                disabledReason: permissionDeniedReason(permissions.read),
              },
            ]}
          /></StopRowClick>
      ),
      sortable: false,
      align: 'center' as const,
    },
  ], [clusterId, permissions.read]);

  return (
    <>
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => r.name}
        onRowClick={makeRowClick(router, clusterId, 'storageclasses', permissions.read)}
        searchPlaceholder="Search storage classes..." loading={isLoading} emptyMessage="No storage classes found" />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} />
      )}
    </>
  );
}

/**
 * Map from our resource page slugs to the k8s-paths resource type keys
 * for generic resources that need YAML view support.
 */
const genericResourceToK8sType: Record<string, string> = {
  jobs: 'jobs',
  cronjobs: 'cronjobs',
  configmaps: 'configmaps',
  secrets: 'secrets',
  hpa: 'hpa',
  resourcequotas: 'resourcequotas',
  limitranges: 'limitranges',
  poddisruptionbudgets: 'poddisruptionbudgets',
  crds: 'crds',
  serviceaccounts: 'serviceaccounts',
  'k8s-clusterroles': 'k8s-clusterroles',
  'k8s-clusterrolebindings': 'k8s-clusterrolebindings',
  'k8s-roles': 'k8s-roles',
  'k8s-rolebindings': 'k8s-rolebindings',
  endpoints: 'endpoints',
  replicasets: 'replicasets',
};

/** Resource types that have a Create button via YAML templates */
const creatableGenericTypes: Record<string, { templateKey: string; label: string }> = {
  configmaps: { templateKey: 'configmap', label: 'Create ConfigMap' },
  secrets: { templateKey: 'secret', label: 'Create Secret' },
  jobs: { templateKey: 'job', label: 'Create Job' },
  cronjobs: { templateKey: 'cronjob', label: 'Create CronJob' },
};

/** Resource types where delete is supported */
const deletableGenericTypes = new Set([
  'jobs', 'cronjobs', 'configmaps', 'secrets', 'hpa',
  'resourcequotas', 'limitranges', 'poddisruptionbudgets',
  'serviceaccounts', 'k8s-roles', 'k8s-rolebindings',
  'endpoints', 'replicasets',
]);

/** Resource types where edit YAML is supported */
const editableGenericTypes = new Set([
  'configmaps', 'secrets', 'jobs', 'cronjobs', 'hpa',
  'resourcequotas', 'limitranges', 'poddisruptionbudgets',
  'serviceaccounts', 'k8s-roles', 'k8s-rolebindings',
  'replicasets',
]);

function GenericResourceTable({ clusterId, resourceType, title }: { clusterId: string; resourceType: string; title: string }) {
  const { data, isLoading } = useGenericResources(clusterId, resourceType);
  const router = useRouter();
  const k8sDeleteMut = useK8sDelete();
  const permissions = useClusterResourcePermissions(clusterId, resourceType);
  const [yamlTarget, setYamlTarget] = useState<{ path: string; title: string } | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<GenericK8sResource | null>(null);
  const [showCreate, setShowCreate] = useState(false);
  const creatableConfig = creatableGenericTypes[resourceType];

  const baseColumns = genericColumnMap[resourceType] || configMapColumns;
  const k8sType = genericResourceToK8sType[resourceType];
  const isDeletable = deletableGenericTypes.has(resourceType);
  const isEditable = editableGenericTypes.has(resourceType);

  const columns = useMemo<Column<GenericK8sResource>[]>(() => {
    if (!k8sType) return baseColumns;
    return [
      // Override the shared name cell with a drill-down link (k8sType === detailHref key).
      nameColumn<GenericK8sResource>(clusterId, k8sType),
      ...baseColumns.slice(1),
      {
        key: 'actions',
        header: '',
        accessor: (row) => {
          const items: ActionMenuItem[] = [
            {
              label: 'View YAML',
              icon: <Code className="h-3.5 w-3.5" />,
              onClick: () => {
                try {
                  const path = row.namespace
                    ? k8sResourcePath(k8sType, row.name, row.namespace)
                    : k8sResourcePath(k8sType, row.name);
                  setYamlTarget({ path, title: `${title}: ${row.namespace ? row.namespace + '/' : ''}${row.name}` });
                } catch {
                  toastError('YAML view not available for this resource type');
                }
              },
              disabled: !permissions.read.allowed,
              disabledReason: permissionDeniedReason(permissions.read),
            },
          ];
          if (isEditable) {
            items.push({
              label: 'Edit YAML',
              icon: <Pencil className="h-3.5 w-3.5" />,
              onClick: () => {
                try {
                  const path = row.namespace
                    ? k8sResourcePath(k8sType, row.name, row.namespace)
                    : k8sResourcePath(k8sType, row.name);
                  setYamlTarget({ path, title: `${title}: ${row.namespace ? row.namespace + '/' : ''}${row.name}` });
                } catch {
                  toastError('Edit not available for this resource type');
                }
              },
              disabled: !permissions.update.allowed,
              disabledReason: permissionDeniedReason(permissions.update),
            });
          }
          if (isDeletable) {
            items.push({
              label: 'Delete',
              icon: <Trash2 className="h-3.5 w-3.5" />,
              onClick: () => setDeleteTarget(row),
              variant: 'destructive',
              disabled: !permissions.delete.allowed,
              disabledReason: permissionDeniedReason(permissions.delete),
              separator: true,
            });
          }
          return <StopRowClick><ActionMenu items={items} /></StopRowClick>;
        },
        sortable: false,
        align: 'center' as const,
      },
    ];
  }, [clusterId, baseColumns, k8sType, isDeletable, isEditable, permissions.delete, permissions.read, permissions.update, title]);

  return (
    <>
      {creatableConfig && (
        <div className="flex justify-end mb-4">
          <ActionButton
            size="sm"
            intent="primary"
            icon={<Plus className="h-3.5 w-3.5" />}
            onClick={() => setShowCreate(true)}
            disabled={!permissions.create.allowed}
            disabledReason={permissionDeniedReason(permissions.create)}
          >
            {creatableConfig.label}
          </ActionButton>
        </div>
      )}
      <DataTable data={data || []} columns={columns} keyExtractor={(r) => r.namespace ? `${r.namespace}/${r.name}` : r.name}
        onRowClick={k8sType ? makeRowClick(router, clusterId, k8sType, permissions.read) : undefined}
        searchPlaceholder={`Search ${title.toLowerCase()}...`} loading={isLoading} emptyMessage={`No ${title.toLowerCase()} found`} />
      {yamlTarget && (
        <YamlViewDialog open={!!yamlTarget} onClose={() => setYamlTarget(null)} clusterId={clusterId}
          k8sPath={yamlTarget.path} title={yamlTarget.title} allowEdit={isEditable && permissions.update.allowed} />
      )}
      <ConfirmDialog
        open={!!deleteTarget} onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!permissions.delete.allowed) {
            toastPermissionDenied(permissions.delete);
            return;
          }
          if (deleteTarget && k8sType) {
            const path = deleteTarget.namespace
              ? k8sResourcePath(k8sType, deleteTarget.name, deleteTarget.namespace)
              : k8sResourcePath(k8sType, deleteTarget.name);
            k8sDeleteMut.mutate({ clusterId, path }, { onSuccess: () => setDeleteTarget(null) });
          }
        }}
        title={`Delete ${title.replace(/s$/, '')}`}
        description={`This will permanently delete ${deleteTarget?.name}.`}
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={k8sDeleteMut.isPending}
      />
      {creatableConfig && resourceType === 'configmaps' && (
        // DIR-02: schema-lite form for ConfigMap; YAML still available via template dialog for other kinds.
        <ConfigMapFormDialog open={showCreate} onClose={() => setShowCreate(false)} clusterId={clusterId} />
      )}
      {creatableConfig && resourceType !== 'configmaps' && (
        <CreateResourceDialog open={showCreate} onClose={() => setShowCreate(false)} clusterId={clusterId}
          templateKey={creatableConfig.templateKey} title={creatableConfig.label} />
      )}
    </>
  );
}

// ── Resource config ──

const resourceTitles: Record<string, string> = {
  nodes: 'Nodes',
  namespaces: 'Namespaces',
  events: 'Events',
  deployments: 'Deployments',
  daemonsets: 'DaemonSets',
  statefulsets: 'StatefulSets',
  jobs: 'Jobs',
  cronjobs: 'CronJobs',
  pods: 'Pods',
  services: 'Services',
  ingresses: 'Ingresses',
  networkpolicies: 'Network Policies',
  hpa: 'Horizontal Pod Autoscalers',
  persistentvolumes: 'Persistent Volumes',
  persistentvolumeclaims: 'Persistent Volume Claims',
  storageclasses: 'Storage Classes',
  configmaps: 'ConfigMaps',
  secrets: 'Secrets',
  resourcequotas: 'Resource Quotas',
  limitranges: 'Limit Ranges',
  poddisruptionbudgets: 'Pod Disruption Budgets',
  crds: 'Custom Resource Definitions',
  serviceaccounts: 'Service Accounts',
  'k8s-clusterroles': 'ClusterRoles',
  'k8s-clusterrolebindings': 'ClusterRoleBindings',
  'k8s-roles': 'Roles',
  'k8s-rolebindings': 'RoleBindings',
  endpoints: 'Endpoints',
  replicasets: 'ReplicaSets',
  // Gateway API
  gateways: 'Gateways',
  httproutes: 'HTTPRoutes',
  gatewayclasses: 'Gateway Classes',
  grpcroutes: 'GRPCRoutes',
  tlsroutes: 'TLSRoutes',
  tcproutes: 'TCPRoutes',
  udproutes: 'UDPRoutes',
  referencegrants: 'Reference Grants',
};

const workloadKinds: Record<string, string> = {
  deployments: 'Deployment',
  daemonsets: 'DaemonSet',
  statefulsets: 'StatefulSet',
  jobs: 'Job',
  cronjobs: 'CronJob',
};

// Resource types that use the generic endpoint
const genericResourceTypes = new Set([
  'jobs', 'cronjobs', 'configmaps', 'secrets', 'hpa',
  'resourcequotas', 'limitranges', 'poddisruptionbudgets',
  'crds', 'serviceaccounts',
  'k8s-clusterroles', 'k8s-clusterrolebindings',
  'k8s-roles', 'k8s-rolebindings',
  'endpoints', 'replicasets',
]);

// ── Main Page Component ──

function ClusterResourcePage() {
  const params = useParams();
  const clusterId = params.id as string;
  const resource = params.resource as string;

  const title = resourceTitles[resource];
  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);

  // The agent doesn't yet emit per-kind change events (full informer fan-
  // out is a follow-up). Until then we listen for the coarse
  // `cluster.k8s_changed` signal plus cluster-lifecycle events and refetch
  // the resource list whose query keys live under this cluster id.
  useLiveQueryInvalidation(
    [
      'cluster.k8s_changed',
      'cluster.connected',
      'cluster.disconnected',
      'cluster.heartbeat',
      'cluster.status_changed',
    ],
    [
      // Prefix matches every per-resource list under this cluster
      // (workloads, pods, namespaces, events, services, ingresses, etc.).
      ['clusters', clusterId],
      ['workloads', clusterId],
      ['storage', clusterId],
      ['networking', clusterId],
      ['generic', clusterId],
      queryKeys.clusters.detail(clusterId),
    ],
  );

  // Precise, immediate liveness for the kind actually on screen: watch its k8s
  // list stream and refetch this cluster's lists the moment it changes, instead
  // of waiting for the coarse cluster-wide signal above (which the agent emits
  // only on a heartbeat cadence). Only core/apps/batch/networking kinds with a
  // known API path are watchable; unknown or custom types fall through to the
  // coarse signal + polling. React Query only refetches *active* queries, so a
  // change to the on-screen kind refreshes just that table.
  const watchDef = getResourceDef(resource);
  useResourceWatchInvalidation({
    clusterId,
    path: watchDef ? k8sListPath(resource) : '',
    queryKeys: [
      ['clusters', clusterId],
      ['workloads', clusterId],
      ['storage', clusterId],
      ['networking', clusterId],
      ['generic', clusterId],
    ],
    enabled: !!clusterId && !!watchDef,
  });

  if (clusterLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!cluster || !title) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>{!title ? 'Unknown resource type' : 'Cluster not found'}</p>
      </div>
    );
  }

  const renderTable = () => {
    // Generic resources use the generic endpoint
    if (genericResourceTypes.has(resource)) {
      return <GenericResourceTable clusterId={clusterId} resourceType={resource} title={title} />;
    }
    switch (resource) {
      case 'nodes': return <NodesTable clusterId={clusterId} />;
      case 'namespaces': return <NamespacesTable clusterId={clusterId} />;
      case 'events': return <EventsTable clusterId={clusterId} />;
      case 'pods': return <PodsTable clusterId={clusterId} />;
      case 'deployments':
      case 'daemonsets':
      case 'statefulsets':
        return <WorkloadsTable clusterId={clusterId} kind={workloadKinds[resource]} title={title} />;
      case 'services': return <ServicesTable clusterId={clusterId} />;
      case 'ingresses': return <IngressesTable clusterId={clusterId} />;
      case 'networkpolicies': return <NetworkPoliciesTable clusterId={clusterId} />;
      case 'gateways': return <GatewaysTable clusterId={clusterId} />;
      case 'httproutes': return <HTTPRoutesTable clusterId={clusterId} />;
      case 'gatewayclasses': return <GatewayClassesTable clusterId={clusterId} />;
      case 'grpcroutes': return <GRPCRoutesTable clusterId={clusterId} />;
      case 'tlsroutes': return <TLSRoutesTable clusterId={clusterId} />;
      case 'tcproutes': return <TCPRoutesTable clusterId={clusterId} />;
      case 'udproutes': return <UDPRoutesTable clusterId={clusterId} />;
      case 'referencegrants': return <ReferenceGrantsTable clusterId={clusterId} />;
      case 'persistentvolumes': return <PVsTable clusterId={clusterId} />;
      case 'persistentvolumeclaims': return <PVCsTable clusterId={clusterId} />;
      case 'storageclasses': return <StorageClassesTable clusterId={clusterId} />;
      default: return <div className="py-16 text-center text-muted-foreground">Unknown resource type</div>;
    }
  };

  return (
    <div className="space-y-4">
      <h1 className="text-xl font-semibold text-foreground tracking-tight">{title}</h1>
      {renderTable()}
    </div>
  );
}

export const Route = createFileRoute('/dashboard/clusters/$id/$resource/')({
  component: ClusterResourcePage,
});
