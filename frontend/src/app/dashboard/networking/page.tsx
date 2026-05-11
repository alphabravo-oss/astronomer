'use client';

import { useState } from 'react';
import {
  useServices,
  useIngresses,
  useNetworkPolicies,
  useDeleteService,
  useDeleteIngress,
  useDeleteNetworkPolicy,
  useClusters,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { K8sService, Ingress, NetworkPolicy } from '@/types';
import {
  Globe,
  Network,
  ShieldCheck,
  Server,
  Trash2,
  ChevronDown,
} from 'lucide-react';

type TabKey = 'services' | 'ingresses' | 'networkpolicies';

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'services', label: 'Services', icon: Globe },
  { key: 'ingresses', label: 'Ingresses', icon: Network },
  { key: 'networkpolicies', label: 'Network Policies', icon: ShieldCheck },
];

const serviceTypeColors: Record<string, string> = {
  ClusterIP: 'bg-muted text-muted-foreground',
  NodePort: 'bg-status-info/10 text-status-info',
  LoadBalancer: 'bg-status-success/10 text-status-success',
  ExternalName: 'bg-status-warning/10 text-status-warning',
};

export default function NetworkingPage() {
  const [activeTab, setActiveTab] = useState<TabKey>('services');
  const [selectedClusterId, setSelectedClusterId] = useState('');

  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];

  const { data: services, isLoading: servicesLoading } = useServices(selectedClusterId);
  const { data: ingresses, isLoading: ingressesLoading } = useIngresses(selectedClusterId);
  const { data: networkPolicies, isLoading: npLoading } = useNetworkPolicies(selectedClusterId);

  const deleteService = useDeleteService();
  const deleteIngress = useDeleteIngress();
  const deleteNetworkPolicy = useDeleteNetworkPolicy();

  const serviceColumns: Column<K8sService>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Globe className="h-4 w-4 text-muted-foreground" />
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
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className={cn('text-xs px-2 py-0.5 rounded font-medium', serviceTypeColors[row.type] || 'bg-muted text-muted-foreground')}>
          {row.type}
        </span>
      ),
    },
    {
      key: 'clusterIP',
      header: 'Cluster IP',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground font-mono text-xs">{row.clusterIP}</span>
      ),
    },
    {
      key: 'ports',
      header: 'Ports',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.ports.slice(0, 3).map((port, idx) => (
            <span key={idx} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
              {port.port}{port.nodePort ? `:${port.nodePort}` : ''}/{port.protocol}
            </span>
          ))}
          {row.ports.length > 3 && (
            <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              +{row.ports.length - 3}
            </span>
          )}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'age',
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
              if (confirm('Delete this service?')) {
                deleteService.mutate({ clusterId: row.clusterId, namespace: row.namespace, name: row.name });
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete service"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const ingressColumns: Column<Ingress>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Network className="h-4 w-4 text-muted-foreground" />
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
      key: 'hosts',
      header: 'Hosts',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.hosts.slice(0, 3).map((host) => (
            <span key={host} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
              {host}
            </span>
          ))}
          {row.hosts.length > 3 && (
            <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              +{row.hosts.length - 3}
            </span>
          )}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'tls',
      header: 'TLS',
      accessor: (row) => (
        <StatusBadge
          status={row.tls ? 'active' : 'disconnected'}
          label={row.tls ? 'Yes' : 'No'}
          showDot={false}
        />
      ),
    },
    {
      key: 'ingressClass',
      header: 'Class',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.ingressClass || '--'}</span>
      ),
    },
    {
      key: 'age',
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
              if (confirm('Delete this ingress?')) {
                deleteIngress.mutate({ clusterId: row.clusterId, namespace: row.namespace, name: row.name });
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete ingress"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const npColumns: Column<NetworkPolicy>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-4 w-4 text-muted-foreground" />
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
      key: 'podSelector',
      header: 'Pod Selector',
      accessor: (row) => {
        const entries = Object.entries(row.podSelector);
        if (entries.length === 0) {
          return <span className="text-xs text-muted-foreground">(all pods)</span>;
        }
        return (
          <div className="flex flex-wrap gap-1">
            {entries.slice(0, 2).map(([k, v]) => (
              <span key={k} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                {k}={v}
              </span>
            ))}
            {entries.length > 2 && (
              <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                +{entries.length - 2}
              </span>
            )}
          </div>
        );
      },
      sortable: false,
    },
    {
      key: 'policyTypes',
      header: 'Policy Types',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.policyTypes.map((type) => (
            <span
              key={type}
              className={cn(
                'text-xs px-2 py-0.5 rounded font-medium',
                type === 'Ingress' ? 'bg-status-info/10 text-status-info' : 'bg-status-warning/10 text-status-warning'
              )}
            >
              {type}
            </span>
          ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'ingressRules',
      header: 'Ingress Rules',
      accessor: (row) => <span className="tabular-nums text-sm">{row.ingressRules}</span>,
      sortAccessor: (row) => row.ingressRules,
      align: 'center',
    },
    {
      key: 'egressRules',
      header: 'Egress Rules',
      accessor: (row) => <span className="tabular-nums text-sm">{row.egressRules}</span>,
      sortAccessor: (row) => row.egressRules,
      align: 'center',
    },
    {
      key: 'age',
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
              if (confirm('Delete this network policy?')) {
                deleteNetworkPolicy.mutate({ clusterId: row.clusterId, namespace: row.namespace, name: row.name });
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete network policy"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Networking</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Services, ingresses, and network policies
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
              Choose a cluster from the dropdown above to view networking resources
            </p>
          </div>
        ) : (
          <>
            {activeTab === 'services' && (
              <DataTable
                data={services || []}
                columns={serviceColumns}
                keyExtractor={(row) => `${row.clusterId}-${row.namespace}-${row.name}`}
                searchPlaceholder="Search services..."
                loading={servicesLoading}
                emptyMessage="No services found"
              />
            )}

            {activeTab === 'ingresses' && (
              <DataTable
                data={ingresses || []}
                columns={ingressColumns}
                keyExtractor={(row) => `${row.clusterId}-${row.namespace}-${row.name}`}
                searchPlaceholder="Search ingresses..."
                loading={ingressesLoading}
                emptyMessage="No ingresses found"
              />
            )}

            {activeTab === 'networkpolicies' && (
              <DataTable
                data={networkPolicies || []}
                columns={npColumns}
                keyExtractor={(row) => `${row.clusterId}-${row.namespace}-${row.name}`}
                searchPlaceholder="Search network policies..."
                loading={npLoading}
                emptyMessage="No network policies found"
              />
            )}
          </>
        )}
      </div>
    </div>
  );
}
