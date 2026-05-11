'use client';

import { useState } from 'react';
import {
  usePersistentVolumes,
  usePersistentVolumeClaims,
  useStorageClasses,
  useDeletePV,
  useDeletePVC,
  useClusters,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { PersistentVolume, PersistentVolumeClaim, StorageClass } from '@/types';
import {
  HardDrive,
  Database,
  Layers,
  Server,
  Trash2,
  ChevronDown,
} from 'lucide-react';

type TabKey = 'pvs' | 'pvcs' | 'storageclasses';

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'pvs', label: 'Persistent Volumes', icon: HardDrive },
  { key: 'pvcs', label: 'Persistent Volume Claims', icon: Database },
  { key: 'storageclasses', label: 'Storage Classes', icon: Layers },
];

export default function StoragePage() {
  const [activeTab, setActiveTab] = useState<TabKey>('pvs');
  const [selectedClusterId, setSelectedClusterId] = useState('');

  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];

  const { data: pvs, isLoading: pvsLoading } = usePersistentVolumes(selectedClusterId);
  const { data: pvcs, isLoading: pvcsLoading } = usePersistentVolumeClaims(selectedClusterId);
  const { data: storageClasses, isLoading: scLoading } = useStorageClasses(selectedClusterId);

  const deletePV = useDeletePV();
  const deletePVC = useDeletePVC();

  const pvColumns: Column<PersistentVolume>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <HardDrive className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>
        </div>
      ),
    },
    {
      key: 'capacity',
      header: 'Capacity',
      accessor: (row) => <span className="tabular-nums text-sm">{row.capacity}</span>,
    },
    {
      key: 'accessModes',
      header: 'Access Modes',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.accessModes.map((mode) => (
            <span key={mode} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              {mode}
            </span>
          ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'reclaimPolicy',
      header: 'Reclaim Policy',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
          {row.reclaimPolicy}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status.toLowerCase()} label={row.status} />,
    },
    {
      key: 'storageClass',
      header: 'Storage Class',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.storageClass || '--'}</span>
      ),
    },
    {
      key: 'created',
      header: 'Age',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => {
              if (confirm('Delete this persistent volume?')) {
                deletePV.mutate({ clusterId: row.clusterId, name: row.name });
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete PV"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const pvcColumns: Column<PersistentVolumeClaim>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Database className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>
        </div>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
          {row.namespace}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status.toLowerCase()} label={row.status} />,
    },
    {
      key: 'volume',
      header: 'Volume',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground font-mono text-xs">
          {row.volumeName || '--'}
        </span>
      ),
    },
    {
      key: 'capacity',
      header: 'Capacity',
      accessor: (row) => <span className="tabular-nums text-sm">{row.capacity}</span>,
    },
    {
      key: 'accessModes',
      header: 'Access Modes',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.accessModes.map((mode) => (
            <span key={mode} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              {mode}
            </span>
          ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'storageClass',
      header: 'Storage Class',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.storageClass || '--'}</span>
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => {
              if (confirm('Delete this persistent volume claim?')) {
                deletePVC.mutate({ clusterId: row.clusterId, namespace: row.namespace, name: row.name });
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete PVC"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const scColumns: Column<StorageClass>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Layers className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground font-mono text-xs">{row.name}</span>
          {row.isDefault && (
            <span className="text-xs px-2 py-0.5 rounded bg-status-info/10 text-status-info font-medium">
              Default
            </span>
          )}
        </div>
      ),
    },
    {
      key: 'provisioner',
      header: 'Provisioner',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground font-mono text-xs">{row.provisioner}</span>
      ),
    },
    {
      key: 'reclaimPolicy',
      header: 'Reclaim Policy',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
          {row.reclaimPolicy}
        </span>
      ),
    },
    {
      key: 'volumeBindingMode',
      header: 'Volume Binding Mode',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.volumeBindingMode}</span>
      ),
    },
    {
      key: 'allowExpansion',
      header: 'Allow Expansion',
      accessor: (row) => (
        <StatusBadge
          status={row.allowVolumeExpansion ? 'active' : 'disconnected'}
          label={row.allowVolumeExpansion ? 'Yes' : 'No'}
          showDot={false}
        />
      ),
    },
    {
      key: 'created',
      header: 'Age',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{formatRelativeTime(row.createdAt)}</span>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Storage</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Persistent volumes, claims, and storage classes
          </p>
        </div>
        <div className="relative">
          <select
            value={selectedClusterId}
            onChange={(e) => setSelectedClusterId(e.target.value)}
            className="h-9 pl-3 pr-8 rounded-md border border-border bg-background text-sm
              text-foreground appearance-none cursor-pointer
              focus:outline-none focus:ring-1 focus:ring-ring"
          >
            <option value="">Select a cluster</option>
            {clusters.map((cluster) => (
              <option key={cluster.id} value={cluster.id}>
                {cluster.displayName}
              </option>
            ))}
          </select>
          <ChevronDown className="absolute right-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-6">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            return (
              <button
                key={tab.key}
                onClick={() => setActiveTab(tab.key)}
                className={cn(
                  'flex items-center gap-2 pb-3 text-sm font-medium border-b-2 transition-colors',
                  activeTab === tab.key
                    ? 'border-foreground text-foreground'
                    : 'border-transparent text-muted-foreground hover:text-foreground'
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </button>
            );
          })}
        </nav>
      </div>

      {/* Content */}
      <div className="animate-fade-in">
        {!selectedClusterId ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <Server className="h-12 w-12 text-muted-foreground/30 mb-4" />
            <h3 className="text-lg font-medium text-foreground">Select a cluster</h3>
            <p className="text-sm text-muted-foreground mt-1">
              Choose a cluster from the dropdown above to view storage resources
            </p>
          </div>
        ) : (
          <>
            {activeTab === 'pvs' && (
              <DataTable
                data={pvs || []}
                columns={pvColumns}
                keyExtractor={(row) => `${row.clusterId}-${row.name}`}
                searchPlaceholder="Search persistent volumes..."
                loading={pvsLoading}
                emptyMessage="No persistent volumes found"
              />
            )}

            {activeTab === 'pvcs' && (
              <DataTable
                data={pvcs || []}
                columns={pvcColumns}
                keyExtractor={(row) => `${row.clusterId}-${row.namespace}-${row.name}`}
                searchPlaceholder="Search persistent volume claims..."
                loading={pvcsLoading}
                emptyMessage="No persistent volume claims found"
              />
            )}

            {activeTab === 'storageclasses' && (
              <DataTable
                data={storageClasses || []}
                columns={scColumns}
                keyExtractor={(row) => `${row.clusterId}-${row.name}`}
                searchPlaceholder="Search storage classes..."
                loading={scLoading}
                emptyMessage="No storage classes found"
              />
            )}
          </>
        )}
      </div>
    </div>
  );
}
