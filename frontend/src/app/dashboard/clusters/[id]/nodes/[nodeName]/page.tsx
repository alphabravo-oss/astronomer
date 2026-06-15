'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { useParams, useRouter } from '@/lib/navigation';
import { useState } from 'react';
import { useNodeDetail } from '@/lib/hooks';
import * as apiClient from '@/lib/api';
import { StatusBadge } from '@/components/ui/status-badge';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ActionButton } from '@/components/ui/action-button';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { YamlViewDialog } from '@/components/ui/yaml-view-dialog';
import { ResourceActions } from '@/components/workloads/resource-actions';
import { k8sResourcePath } from '@/lib/k8s-paths';
import { usePermissionDecision } from '@/lib/permission-hooks';
import { formatBytes, formatCPU, formatRelativeTime, cn } from '@/lib/utils';
import type { NodePod, NodeEvent, NodeTaint, NodeImage, NodeDetailCondition } from '@/types';
import {
  Loader2,
  ArrowLeft,
  Cpu,
  MemoryStick,
  Box,
  CheckCircle2,
  XCircle,
  Server,
  Tag,
  Code,
  ShieldBan,
  ShieldCheck,
  Unplug,
  Plus,
  Trash2,
} from 'lucide-react';
import { toastApiError, toastSuccess, toastWarning } from '@/lib/toast';

// ── Tabs ──

const TABS = [
  { id: 'overview', label: 'Overview' },
  { id: 'pods', label: 'Pods' },
  { id: 'conditions', label: 'Conditions' },
  { id: 'info', label: 'Info' },
  { id: 'taints', label: 'Taints' },
  { id: 'images', label: 'Images' },
  { id: 'events', label: 'Events' },
] as const;

type TabId = (typeof TABS)[number]['id'];

// ── Column Definitions ──

const podColumns: Column<NodePod>[] = [
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
    key: 'image',
    header: 'Image',
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono truncate max-w-[220px] block">
        {row.images?.[0] || '-'}
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

const conditionColumns: Column<NodeDetailCondition>[] = [
  {
    key: 'type',
    header: 'Type',
    accessor: (row) => <span className="font-medium text-foreground text-xs">{row.type}</span>,
  },
  {
    key: 'status',
    header: 'Status',
    accessor: (row) => {
      const isHealthy =
        (row.type === 'Ready' && row.status === 'True') ||
        (row.type !== 'Ready' && row.status === 'False');
      return (
        <div className="flex items-center gap-1.5">
          {isHealthy ? (
            <CheckCircle2 className="h-3.5 w-3.5 text-status-success" />
          ) : (
            <XCircle className="h-3.5 w-3.5 text-status-error" />
          )}
          <span className="text-xs">{row.status}</span>
        </div>
      );
    },
  },
  {
    key: 'reason',
    header: 'Reason',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.reason || '-'}</span>,
  },
  {
    key: 'message',
    header: 'Message',
    accessor: (row) => <span className="text-xs text-muted-foreground line-clamp-2">{row.message || '-'}</span>,
    sortable: false,
  },
  {
    key: 'lastHeartbeat',
    header: 'Last Heartbeat',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : '-'}</span>,
  },
  {
    key: 'lastTransition',
    header: 'Last Transition',
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.lastTransition ? formatRelativeTime(row.lastTransition) : '-'}</span>,
  },
];

const taintColumns: Column<NodeTaint>[] = [
  {
    key: 'key',
    header: 'Key',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs">{row.key}</span>,
  },
  {
    key: 'value',
    header: 'Value',
    accessor: (row) => <span className="text-xs text-muted-foreground font-mono">{row.value || '-'}</span>,
  },
  {
    key: 'effect',
    header: 'Effect',
    accessor: (row) => (
      <span className={cn(
        'px-1.5 py-0.5 rounded text-2xs',
        row.effect === 'NoSchedule' ? 'bg-status-warning/10 text-status-warning' :
        row.effect === 'NoExecute' ? 'bg-status-error/10 text-status-error' :
        'bg-muted text-muted-foreground'
      )}>
        {row.effect}
      </span>
    ),
  },
];

const imageColumns: Column<NodeImage>[] = [
  {
    key: 'name',
    header: 'Image',
    accessor: (row) => <span className="font-medium text-foreground font-mono text-xs truncate max-w-[500px] block">{row.name}</span>,
  },
  {
    key: 'size',
    header: 'Size',
    accessor: (row) => <span className="text-xs text-muted-foreground tabular-nums">{row.sizeBytes > 0 ? formatBytes(row.sizeBytes) : '-'}</span>,
    sortAccessor: (row) => row.sizeBytes,
    align: 'right',
  },
];

const eventColumns: Column<NodeEvent>[] = [
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
    accessor: (row) => <span className="text-xs text-muted-foreground">{row.lastTimestamp ? formatRelativeTime(row.lastTimestamp) : '-'}</span>,
  },
];

// ── Gauge Component ──

function ResourceGauge({
  label,
  icon: Icon,
  used,
  total,
  formatFn,
}: {
  label: string;
  icon: React.ElementType;
  used: number;
  total: number;
  formatFn: (v: number) => string;
}) {
  const pct = total > 0 ? (used / total) * 100 : 0;
  const color = pct >= 90 ? 'bg-status-error' : pct >= 75 ? 'bg-status-warning' : 'bg-status-success';
  const textColor = pct >= 90 ? 'text-status-error' : pct >= 75 ? 'text-status-warning' : 'text-status-success';

  return (
    <div className="bg-card border border-border rounded-lg p-4">
      <div className="flex items-center gap-2 mb-3">
        <Icon className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium text-foreground">{label}</span>
      </div>
      <div className="flex items-end gap-2 mb-2">
        <span className={cn('text-2xl font-bold tabular-nums', textColor)}>
          {Math.round(pct)}%
        </span>
      </div>
      <div className="w-full h-2 bg-muted rounded-full overflow-hidden mb-2">
        <div className={cn('h-full rounded-full transition-all', color)} style={{ width: `${Math.min(pct, 100)}%` }} />
      </div>
      <p className="text-xs text-muted-foreground tabular-nums">
        {formatFn(used)} / {formatFn(total)}
      </p>
    </div>
  );
}

// ── Health Alert ──

function ConditionAlert({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div className={cn(
      'flex items-center gap-2 px-3 py-2 rounded-md border text-xs font-medium',
      ok
        ? 'bg-status-success/5 border-status-success/20 text-status-success'
        : 'bg-status-error/5 border-status-error/20 text-status-error'
    )}>
      {ok ? <CheckCircle2 className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
      {label}
    </div>
  );
}

// ── Main Page ──

export default function NodeDetailPage() {
  const params = useParams();
  const router = useRouter();
  const clusterId = params.id as string;
  const nodeName = params.nodeName as string;
  const [activeTab, setActiveTab] = useState<TabId>('overview');

  const { data: node, isLoading, refetch } = useNodeDetail(clusterId, nodeName);
  const [showYaml, setShowYaml] = useState(false);
  const [showDrain, setShowDrain] = useState(false);
  const [showAddTaint, setShowAddTaint] = useState(false);
  const [newTaint, setNewTaint] = useState({ key: '', value: '', effect: 'NoSchedule' });
  const [showAddLabel, setShowAddLabel] = useState(false);
  const [newLabel, setNewLabel] = useState({ key: '', value: '' });
  const [showAddAnnotation, setShowAddAnnotation] = useState(false);
  const [newAnnotation, setNewAnnotation] = useState({ key: '', value: '' });
  const [nodeActionPending, setNodeActionPending] = useState(false);
  const nodeScope = { type: 'cluster' as const, id: clusterId };
  const nodeUpdateDecision = usePermissionDecision('nodes', 'update', nodeScope);
  const nodeManageDecision = usePermissionDecision('nodes', 'manage', nodeScope);
  const nodeUpdateBlockedReason = nodeUpdateDecision.allowed ? undefined : nodeUpdateDecision.disabledReason;
  const nodeManageBlockedReason = nodeManageDecision.allowed ? undefined : nodeManageDecision.disabledReason;

  const handleCordon = async () => {
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.cordonNode(clusterId, nodeName);
      refetch();
      toastSuccess('Node cordoned');
    } catch (error) {
      toastApiError('Failed to cordon node', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleUncordon = async () => {
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.uncordonNode(clusterId, nodeName);
      refetch();
      toastSuccess('Node uncordoned');
    } catch (error) {
      toastApiError('Failed to uncordon node', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleDrain = async () => {
    if (!nodeManageDecision.allowed) {
      toastWarning(nodeManageDecision.disabledReason || 'Requires nodes:manage');
      return;
    }
    setNodeActionPending(true);
    try {
      const result = await apiClient.drainNode(clusterId, nodeName);
      if (result.status === 'blocked') {
        toastWarning(result.message || `Drain blocked for ${nodeName}`);
      } else if (result.status === 'partial') {
        toastWarning(result.message || `Node ${nodeName} partially drained`);
      } else {
        toastSuccess(result.message || `Node ${nodeName} drained`);
      }
      setShowDrain(false);
      refetch();
    } catch (error) {
      toastApiError('Failed to drain', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleAddTaint = async () => {
    if (!newTaint.key) return;
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.addNodeTaint(clusterId, nodeName, newTaint);
      refetch();
      setShowAddTaint(false);
      setNewTaint({ key: '', value: '', effect: 'NoSchedule' });
      toastSuccess('Taint added');
    } catch (error) {
      toastApiError('Failed to add taint', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleRemoveTaint = async (taint: NodeTaint) => {
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.removeNodeTaint(clusterId, nodeName, { key: taint.key, effect: taint.effect });
      refetch();
      toastSuccess('Taint removed');
    } catch (error) {
      toastApiError('Failed to remove taint', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleAddLabel = async () => {
    if (!newLabel.key) return;
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.setNodeLabel(clusterId, nodeName, newLabel);
      refetch();
      setShowAddLabel(false);
      setNewLabel({ key: '', value: '' });
      toastSuccess('Label added');
    } catch (error) {
      toastApiError('Failed to add label', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleRemoveLabel = async (key: string) => {
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.removeNodeLabel(clusterId, nodeName, { key });
      refetch();
      toastSuccess('Label removed');
    } catch (error) {
      toastApiError('Failed to remove label', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleAddAnnotation = async () => {
    if (!newAnnotation.key) return;
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.setNodeAnnotation(clusterId, nodeName, newAnnotation);
      refetch();
      setShowAddAnnotation(false);
      setNewAnnotation({ key: '', value: '' });
      toastSuccess('Annotation added');
    } catch (error) {
      toastApiError('Failed to add annotation', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  const handleRemoveAnnotation = async (key: string) => {
    if (!nodeUpdateDecision.allowed) {
      toastWarning(nodeUpdateDecision.disabledReason || 'Requires nodes:update');
      return;
    }
    setNodeActionPending(true);
    try {
      await apiClient.removeNodeAnnotation(clusterId, nodeName, { key });
      refetch();
      toastSuccess('Annotation removed');
    } catch (error) {
      toastApiError('Failed to remove annotation', error);
    } finally {
      setNodeActionPending(false);
    }
  };

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }

  if (!node) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Node not found</p>
      </div>
    );
  }

  // Derive condition health
  const condMap = Object.fromEntries(node.conditions.map((c) => [c.type, c.status]));
  const isKubeletOk = condMap['Ready'] === 'True';
  const isMemoryPressureOk = condMap['MemoryPressure'] === 'False';
  const isDiskPressureOk = condMap['DiskPressure'] === 'False';
  const isPidPressureOk = condMap['PIDPressure'] === 'False';

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-start gap-4">
        <button
          onClick={() => router.push(`/dashboard/clusters/${clusterId}/nodes`)}
          className="mt-1 p-1 rounded-md hover:bg-accent transition-colors text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-5 w-5" />
        </button>
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-3 mb-1">
            <h1 className="text-xl font-semibold text-foreground tracking-tight font-mono truncate">{node.name}</h1>
            <StatusBadge status={node.status} />
            {node.unschedulable && (
              <span className="px-2 py-0.5 rounded text-2xs bg-status-warning/10 text-status-warning font-medium">
                Unschedulable
              </span>
            )}
          </div>
          <div className="flex items-center gap-4 text-xs text-muted-foreground">
            <span>Roles: {node.roles.join(', ')}</span>
            <span>Age: {formatRelativeTime(node.createdAt)}</span>
            <span>{node.nodeInfo.kubeletVersion}</span>
          </div>
        </div>

        {/* Node Actions */}
        <div className="flex items-center gap-2 flex-shrink-0">
          <button
            onClick={() => setShowYaml(true)}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded-md text-sm font-medium
              border border-border text-foreground hover:bg-accent transition-colors"
          >
            <Code className="h-3.5 w-3.5" /> YAML
          </button>
          {node.unschedulable ? (
            <ActionButton
              onClick={handleUncordon}
              disabled={nodeActionPending || !nodeUpdateDecision.allowed}
              disabledReason={nodeUpdateBlockedReason}
              size="sm"
              icon={<ShieldCheck className="h-3.5 w-3.5" />}
              className="gap-1.5 text-sm border-status-success/30 text-status-success hover:bg-status-success/10"
            >
              Uncordon
            </ActionButton>
          ) : (
            <ActionButton
              onClick={handleCordon}
              disabled={nodeActionPending || !nodeUpdateDecision.allowed}
              disabledReason={nodeUpdateBlockedReason}
              size="sm"
              icon={<ShieldBan className="h-3.5 w-3.5" />}
              className="gap-1.5 text-sm border-status-warning/30 text-status-warning hover:bg-status-warning/10"
            >
              Cordon
            </ActionButton>
          )}
          <ActionButton
            onClick={() => setShowDrain(true)}
            disabled={nodeActionPending || !nodeManageDecision.allowed}
            disabledReason={nodeManageBlockedReason}
            size="sm"
            icon={<Unplug className="h-3.5 w-3.5" />}
            className="gap-1.5 text-sm border-status-error/30 text-status-error hover:bg-status-error/10"
          >
            Drain
          </ActionButton>
          {/* Node is cluster-scoped — ResourceActions renders only Delete here. */}
          <ResourceActions
            clusterId={clusterId}
            kind="Node"
            name={nodeName}
            onDeleted={() => router.push(`/dashboard/clusters/${clusterId}/nodes`)}
          />
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-0 -mb-px">
          {TABS.map((tab) => (
            <button
              key={tab.id}
              onClick={() => setActiveTab(tab.id)}
              className={cn(
                'px-4 py-2 text-sm font-medium border-b-2 transition-colors',
                activeTab === tab.id
                  ? 'border-foreground text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground hover:border-muted-foreground/30'
              )}
            >
              {tab.label}
              {tab.id === 'pods' && node.pods.length > 0 && (
                <span className="ml-1.5 px-1.5 py-0.5 rounded-full text-2xs bg-muted">{node.pods.length}</span>
              )}
              {tab.id === 'taints' && node.taints.length > 0 && (
                <span className="ml-1.5 px-1.5 py-0.5 rounded-full text-2xs bg-muted">{node.taints.length}</span>
              )}
              {tab.id === 'events' && node.events.length > 0 && (
                <span className="ml-1.5 px-1.5 py-0.5 rounded-full text-2xs bg-muted">{node.events.length}</span>
              )}
            </button>
          ))}
        </nav>
      </div>

      {/* Tab Content */}
      {activeTab === 'overview' && (
        <div className="space-y-6">
          {/* Health Status Alerts */}
          <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
            <ConditionAlert label="Kubelet" ok={isKubeletOk} />
            <ConditionAlert label="Memory Pressure" ok={isMemoryPressureOk} />
            <ConditionAlert label="Disk Pressure" ok={isDiskPressureOk} />
            <ConditionAlert label="PID Pressure" ok={isPidPressureOk} />
          </div>

          {/* Resource Gauges */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
            <ResourceGauge label="CPU" icon={Cpu} used={node.cpuUsage} total={node.cpuCapacity} formatFn={formatCPU} />
            <ResourceGauge label="Memory" icon={MemoryStick} used={node.memoryUsage} total={node.memoryCapacity} formatFn={formatBytes} />
            <ResourceGauge label="Pods" icon={Box} used={node.podCount} total={node.podCapacity} formatFn={(v) => String(v)} />
          </div>

          {/* Addresses */}
          {node.addresses.length > 0 && (
            <div className="bg-card border border-border rounded-lg p-4">
              <h3 className="text-sm font-medium text-foreground mb-3">Addresses</h3>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                {node.addresses.map((addr) => (
                  <div key={`${addr.type}-${addr.address}`} className="flex items-center gap-2">
                    <span className="px-1.5 py-0.5 rounded text-2xs bg-muted text-muted-foreground min-w-[80px] text-center">{addr.type}</span>
                    <span className="text-xs font-mono text-foreground">{addr.address}</span>
                  </div>
                ))}
              </div>
            </div>
          )}

          {/* Labels */}
          <div className="bg-card border border-border rounded-lg p-4">
            <div className="flex items-center justify-between mb-3">
              <div className="flex items-center gap-2">
                <Tag className="h-4 w-4 text-muted-foreground" />
                <h3 className="text-sm font-medium text-foreground">Labels</h3>
                <span className="text-xs text-muted-foreground">({Object.keys(node.labels).length})</span>
              </div>
              <button
                onClick={() => setShowAddLabel(true)}
                disabled={nodeActionPending || !nodeUpdateDecision.allowed}
                title={nodeUpdateBlockedReason}
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Plus className="h-3 w-3" /> Add
              </button>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(node.labels).map(([k, v]) => (
                <span key={k} className="inline-flex items-center gap-1 px-2 py-1 rounded text-2xs bg-muted text-muted-foreground font-mono group">
                  <span className="text-foreground">{k}</span>
                  {v && <span>= {v}</span>}
                  <button
                    disabled={nodeActionPending || !nodeUpdateDecision.allowed}
                    title={nodeUpdateBlockedReason}
                    onClick={() => handleRemoveLabel(k)}
                    className="ml-0.5 opacity-0 group-hover:opacity-100 text-status-error/70 hover:text-status-error transition-opacity disabled:cursor-not-allowed"
                  >
                    <XCircle className="h-3 w-3" />
                  </button>
                </span>
              ))}
            </div>
          </div>

          {/* Annotations */}
          <div className="bg-card border border-border rounded-lg p-4">
            <div className="flex items-center justify-between mb-3">
              <div className="flex items-center gap-2">
                <Code className="h-4 w-4 text-muted-foreground" />
                <h3 className="text-sm font-medium text-foreground">Annotations</h3>
                <span className="text-xs text-muted-foreground">({Object.keys(node.annotations).length})</span>
              </div>
              <button
                onClick={() => setShowAddAnnotation(true)}
                disabled={nodeActionPending || !nodeUpdateDecision.allowed}
                title={nodeUpdateBlockedReason}
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Plus className="h-3 w-3" /> Add
              </button>
            </div>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(node.annotations).map(([k, v]) => (
                <span key={k} className="inline-flex items-center gap-1 px-2 py-1 rounded text-2xs bg-muted text-muted-foreground font-mono group">
                  <span className="text-foreground">{k}</span>
                  {v && <span>= {v}</span>}
                  <button
                    disabled={nodeActionPending || !nodeUpdateDecision.allowed}
                    title={nodeUpdateBlockedReason}
                    onClick={() => handleRemoveAnnotation(k)}
                    className="ml-0.5 opacity-0 group-hover:opacity-100 text-status-error/70 hover:text-status-error transition-opacity disabled:cursor-not-allowed"
                  >
                    <XCircle className="h-3 w-3" />
                  </button>
                </span>
              ))}
            </div>
          </div>
        </div>
      )}

      {activeTab === 'pods' && (
        <DataTable
          data={node.pods}
          columns={podColumns}
          keyExtractor={(r) => `${r.namespace}/${r.name}`}
          searchPlaceholder="Search pods..."
          emptyMessage="No pods running on this node"
        />
      )}

      {activeTab === 'conditions' && (
        <DataTable
          data={node.conditions}
          columns={conditionColumns}
          keyExtractor={(r) => r.type}
          emptyMessage="No conditions reported"
        />
      )}

      {activeTab === 'info' && (
        <div className="bg-card border border-border rounded-lg overflow-hidden">
          <Table className="w-full">
            <TableBody className="divide-y divide-border">
              {[
                ['Machine ID', node.nodeInfo.machineId],
                ['System UUID', node.nodeInfo.systemUuid],
                ['Boot ID', node.nodeInfo.bootId],
                ['Kernel Version', node.nodeInfo.kernelVersion],
                ['OS Image', node.nodeInfo.osImage],
                ['Container Runtime', node.nodeInfo.containerRuntimeVersion],
                ['Kubelet Version', node.nodeInfo.kubeletVersion],
                ['Kube-Proxy Version', node.nodeInfo.kubeProxyVersion],
                ['Operating System', node.nodeInfo.operatingSystem],
                ['Architecture', node.nodeInfo.architecture],
              ].map(([label, value]) => (
                <TableRow key={label}>
                  <TableCell className="px-4 py-2.5 text-xs font-medium text-muted-foreground w-48">{label}</TableCell>
                  <TableCell className="px-4 py-2.5 text-xs text-foreground font-mono">{value || '-'}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {activeTab === 'taints' && (
        <div className="space-y-3">
          <div className="flex justify-end">
            <button
              onClick={() => setShowAddTaint(true)}
              disabled={nodeActionPending || !nodeUpdateDecision.allowed}
              title={nodeUpdateBlockedReason}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                bg-primary text-primary-foreground hover:bg-primary/90 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
            >
              <Plus className="h-3.5 w-3.5" /> Add Taint
            </button>
          </div>
          <DataTable
            data={node.taints}
            columns={[
              ...taintColumns,
              {
                key: 'actions',
                header: '',
                accessor: (row) => (
                  <button
                    onClick={() => handleRemoveTaint(row)}
                    disabled={nodeActionPending || !nodeUpdateDecision.allowed}
                    title={nodeUpdateBlockedReason}
                    className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors disabled:cursor-not-allowed disabled:opacity-50"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                ),
                sortable: false,
                align: 'center' as const,
              },
            ]}
            keyExtractor={(r) => `${r.key}-${r.effect}`}
            emptyMessage="No taints on this node"
          />
        </div>
      )}

      {activeTab === 'images' && (
        <DataTable
          data={node.images}
          columns={imageColumns}
          keyExtractor={(r) => r.name}
          searchPlaceholder="Search images..."
          emptyMessage="No images cached on this node"
        />
      )}

      {activeTab === 'events' && (
        <DataTable
          data={node.events}
          columns={eventColumns}
          keyExtractor={(r) => `${r.reason}-${r.lastTimestamp}`}
          searchPlaceholder="Search events..."
          emptyMessage="No events for this node"
        />
      )}

      {/* YAML Dialog */}
      <YamlViewDialog
        open={showYaml}
        onClose={() => setShowYaml(false)}
        clusterId={clusterId}
        k8sPath={k8sResourcePath('nodes', nodeName)}
        title={`Node: ${nodeName}`}
        allowEdit={nodeUpdateDecision.allowed}
      />

      {/* Drain Confirm Dialog */}
      <ConfirmDialog
        open={showDrain}
        onClose={() => setShowDrain(false)}
        onConfirm={handleDrain}
        title="Drain Node"
        description="This will cordon the node and evict all non-DaemonSet pods. Workloads will be rescheduled to other nodes."
        confirmValue={nodeName}
        confirmText="Drain"
        variant="destructive"
        loading={nodeActionPending}
      />

      {/* Add Taint Dialog */}
      {showAddTaint && (
        <OverlayShell onClose={() => setShowAddTaint(false)}>
          <div className="relative bg-card border border-border rounded-lg shadow-xl max-w-md w-full mx-4 animate-fade-in p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">Add Taint</h3>
            <div className="space-y-3">
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Key</label>
                <input type="text" value={newTaint.key} onChange={(e) => setNewTaint({ ...newTaint, key: e.target.value })}
                  placeholder="node.kubernetes.io/unreachable" autoFocus
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Value</label>
                <input type="text" value={newTaint.value} onChange={(e) => setNewTaint({ ...newTaint, value: e.target.value })}
                  placeholder="(optional)"
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Effect</label>
                <select value={newTaint.effect} onChange={(e) => setNewTaint({ ...newTaint, effect: e.target.value })}
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm focus:outline-none focus:ring-1 focus:ring-ring">
                  <option value="NoSchedule">NoSchedule</option>
                  <option value="PreferNoSchedule">PreferNoSchedule</option>
                  <option value="NoExecute">NoExecute</option>
                </select>
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 mt-4">
              <button onClick={() => setShowAddTaint(false)}
                className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
                Cancel
              </button>
              <button onClick={handleAddTaint} disabled={!newTaint.key || nodeActionPending || !nodeUpdateDecision.allowed}
                title={nodeUpdateBlockedReason}
                className="h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90
                  disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                Add Taint
              </button>
            </div>
          </div>
        </OverlayShell>
      )}

      {/* Add Label Dialog */}
      {showAddLabel && (
        <OverlayShell onClose={() => setShowAddLabel(false)}>
          <div className="relative bg-card border border-border rounded-lg shadow-xl max-w-md w-full mx-4 animate-fade-in p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">Add Label</h3>
            <div className="space-y-3">
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Key</label>
                <input type="text" value={newLabel.key} onChange={(e) => setNewLabel({ ...newLabel, key: e.target.value })}
                  placeholder="app.kubernetes.io/name" autoFocus
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Value</label>
                <input type="text" value={newLabel.value} onChange={(e) => setNewLabel({ ...newLabel, value: e.target.value })}
                  placeholder="my-app"
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 mt-4">
              <button onClick={() => setShowAddLabel(false)}
                className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
                Cancel
              </button>
              <button onClick={handleAddLabel} disabled={!newLabel.key || nodeActionPending || !nodeUpdateDecision.allowed}
                title={nodeUpdateBlockedReason}
                className="h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90
                  disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                Add Label
              </button>
            </div>
          </div>
        </OverlayShell>
      )}

      {/* Add Annotation Dialog */}
      {showAddAnnotation && (
        <OverlayShell onClose={() => setShowAddAnnotation(false)}>
          <div className="relative bg-card border border-border rounded-lg shadow-xl max-w-md w-full mx-4 animate-fade-in p-6">
            <h3 className="text-base font-semibold text-foreground mb-4">Add Annotation</h3>
            <div className="space-y-3">
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Key</label>
                <input type="text" value={newAnnotation.key} onChange={(e) => setNewAnnotation({ ...newAnnotation, key: e.target.value })}
                  placeholder="example.com/owner" autoFocus
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
              <div>
                <label className="block text-xs text-muted-foreground mb-1">Value</label>
                <input type="text" value={newAnnotation.value} onChange={(e) => setNewAnnotation({ ...newAnnotation, value: e.target.value })}
                  placeholder="platform"
                  className="w-full h-8 px-3 rounded border border-border bg-background text-sm font-mono focus:outline-none focus:ring-1 focus:ring-ring" />
              </div>
            </div>
            <div className="flex items-center justify-end gap-2 mt-4">
              <button onClick={() => setShowAddAnnotation(false)}
                className="h-8 px-3 rounded text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors">
                Cancel
              </button>
              <button onClick={handleAddAnnotation} disabled={!newAnnotation.key || nodeActionPending || !nodeUpdateDecision.allowed}
                title={nodeUpdateBlockedReason}
                className="h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90
                  disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                Add Annotation
              </button>
            </div>
          </div>
        </OverlayShell>
      )}
    </div>
  );
}
