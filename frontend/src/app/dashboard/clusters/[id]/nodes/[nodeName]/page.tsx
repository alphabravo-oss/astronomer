'use client';

import { useParams, useRouter } from 'next/navigation';
import { useState } from 'react';
import { useNodeDetail, useK8sPatch } from '@/lib/hooks';
import * as apiClient from '@/lib/api';
import { StatusBadge } from '@/components/ui/status-badge';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { YamlViewDialog } from '@/components/ui/yaml-view-dialog';
import { k8sResourcePath } from '@/lib/k8s-paths';
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
import { toast } from 'sonner';

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
  const k8sPatch = useK8sPatch();
  const [showYaml, setShowYaml] = useState(false);
  const [showDrain, setShowDrain] = useState(false);
  const [showAddTaint, setShowAddTaint] = useState(false);
  const [newTaint, setNewTaint] = useState({ key: '', value: '', effect: 'NoSchedule' });
  const [showAddLabel, setShowAddLabel] = useState(false);
  const [newLabel, setNewLabel] = useState({ key: '', value: '' });

  const handleCordon = () => {
    k8sPatch.mutate(
      { clusterId, path: k8sResourcePath('nodes', nodeName), body: { spec: { unschedulable: true } }, patchType: 'strategic-merge' },
      { onSuccess: () => { refetch(); toast.success('Node cordoned'); } }
    );
  };

  const handleUncordon = () => {
    k8sPatch.mutate(
      { clusterId, path: k8sResourcePath('nodes', nodeName), body: { spec: { unschedulable: false } }, patchType: 'strategic-merge' },
      { onSuccess: () => { refetch(); toast.success('Node uncordoned'); } }
    );
  };

  const handleDrain = async () => {
    try {
      await apiClient.k8sPatch(clusterId, k8sResourcePath('nodes', nodeName), { spec: { unschedulable: true } }, 'strategic-merge');
      const podsResult = await apiClient.k8sGet(clusterId, `api/v1/pods?fieldSelector=spec.nodeName=${nodeName}`);
      const pods = podsResult?.items || [];
      for (const pod of pods) {
        const ownerRefs = pod.metadata?.ownerReferences || [];
        const isDaemonSet = ownerRefs.some((ref: { kind: string }) => ref.kind === 'DaemonSet');
        if (isDaemonSet) continue;
        try {
          await apiClient.k8sCreate(clusterId, `api/v1/namespaces/${pod.metadata.namespace}/pods/${pod.metadata.name}/eviction`,
            { apiVersion: 'policy/v1', kind: 'Eviction', metadata: { name: pod.metadata.name, namespace: pod.metadata.namespace } });
        } catch { /* continue eviction */ }
      }
      toast.success(`Node ${nodeName} drained`);
      setShowDrain(false);
      refetch();
    } catch (error) {
      toast.error(`Failed to drain: ${(error as Error).message}`);
    }
  };

  const handleAddTaint = () => {
    if (!newTaint.key) return;
    const currentTaints = node?.taints || [];
    k8sPatch.mutate(
      {
        clusterId,
        path: k8sResourcePath('nodes', nodeName),
        body: { spec: { taints: [...currentTaints, newTaint] } },
        patchType: 'strategic-merge',
      },
      {
        onSuccess: () => {
          refetch();
          setShowAddTaint(false);
          setNewTaint({ key: '', value: '', effect: 'NoSchedule' });
          toast.success('Taint added');
        },
      }
    );
  };

  const handleRemoveTaint = (taint: NodeTaint) => {
    const updatedTaints = (node?.taints || []).filter(
      (t) => !(t.key === taint.key && t.effect === taint.effect)
    );
    k8sPatch.mutate(
      { clusterId, path: k8sResourcePath('nodes', nodeName), body: { spec: { taints: updatedTaints.length > 0 ? updatedTaints : null } }, patchType: 'merge' },
      { onSuccess: () => { refetch(); toast.success('Taint removed'); } }
    );
  };

  const handleAddLabel = () => {
    if (!newLabel.key) return;
    k8sPatch.mutate(
      { clusterId, path: k8sResourcePath('nodes', nodeName), body: { metadata: { labels: { [newLabel.key]: newLabel.value } } }, patchType: 'strategic-merge' },
      {
        onSuccess: () => {
          refetch();
          setShowAddLabel(false);
          setNewLabel({ key: '', value: '' });
          toast.success('Label added');
        },
      }
    );
  };

  const handleRemoveLabel = (key: string) => {
    // Use JSON Patch to remove a label
    k8sPatch.mutate(
      { clusterId, path: k8sResourcePath('nodes', nodeName), body: [{ op: 'remove', path: `/metadata/labels/${key.replace(/\//g, '~1')}` }], patchType: 'json' },
      { onSuccess: () => { refetch(); toast.success('Label removed'); } }
    );
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
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
              border border-border text-foreground hover:bg-accent transition-colors"
          >
            <Code className="h-3.5 w-3.5" /> YAML
          </button>
          {node.unschedulable ? (
            <button
              onClick={handleUncordon}
              disabled={k8sPatch.isPending}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                border border-status-success/30 text-status-success hover:bg-status-success/10 transition-colors"
            >
              <ShieldCheck className="h-3.5 w-3.5" /> Uncordon
            </button>
          ) : (
            <button
              onClick={handleCordon}
              disabled={k8sPatch.isPending}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                border border-status-warning/30 text-status-warning hover:bg-status-warning/10 transition-colors"
            >
              <ShieldBan className="h-3.5 w-3.5" /> Cordon
            </button>
          )}
          <button
            onClick={() => setShowDrain(true)}
            className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
              border border-status-error/30 text-status-error hover:bg-status-error/10 transition-colors"
          >
            <Unplug className="h-3.5 w-3.5" /> Drain
          </button>
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
                className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs font-medium
                  text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
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
                    onClick={() => handleRemoveLabel(k)}
                    className="ml-0.5 opacity-0 group-hover:opacity-100 text-status-error/70 hover:text-status-error transition-opacity"
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
          <table className="w-full">
            <tbody className="divide-y divide-border">
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
                <tr key={label}>
                  <td className="px-4 py-2.5 text-xs font-medium text-muted-foreground w-48">{label}</td>
                  <td className="px-4 py-2.5 text-xs text-foreground font-mono">{value || '-'}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {activeTab === 'taints' && (
        <div className="space-y-3">
          <div className="flex justify-end">
            <button
              onClick={() => setShowAddTaint(true)}
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
                bg-primary text-primary-foreground hover:bg-primary/90 transition-colors"
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
                    className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
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
        allowEdit
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
      />

      {/* Add Taint Dialog */}
      {showAddTaint && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={() => setShowAddTaint(false)} />
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
              <button onClick={handleAddTaint} disabled={!newTaint.key || k8sPatch.isPending}
                className="h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90
                  disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                Add Taint
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Add Label Dialog */}
      {showAddLabel && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" onClick={() => setShowAddLabel(false)} />
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
              <button onClick={handleAddLabel} disabled={!newLabel.key || k8sPatch.isPending}
                className="h-8 px-4 rounded text-sm font-medium bg-primary text-primary-foreground hover:bg-primary/90
                  disabled:opacity-50 disabled:cursor-not-allowed transition-colors">
                Add Label
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
