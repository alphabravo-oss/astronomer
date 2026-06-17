'use client';

import { useState } from 'react';
import { useTabParam } from '@/lib/use-tab-param';
import {
  useLoggingOutputs,
  useCreateLoggingOutput,
  useTestLoggingOutput,
  useLoggingPipelines,
  useCreateLoggingPipeline,
  useLoggingOperations,
  useRetryLoggingOperation,
  useClusters,
  useClusterNamespaces,
  queryKeys,
} from '@/lib/hooks';
import {
  deleteLoggingOutput,
  updateLoggingOutput,
  deleteLoggingPipeline,
  updateLoggingPipeline,
} from '@/lib/api';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { OverlayShell } from '@/components/ui/overlay-shell';
import { formatRelativeTime, cn } from '@/lib/utils';
import type { LoggingOutput, LoggingPipeline, LoggingOutputType, LoggingOperation } from '@/types';
import {
  FileText,
  Plus,
  GitBranch,
  X,
  Loader2,
  Trash2,
  Send,
  Database,
  Cloud,
  HardDrive,
  Server,
  Activity,
  RotateCcw,
} from 'lucide-react';
import { toastError, toastSuccess } from '@/lib/toast';
import { useQueryClient } from '@tanstack/react-query';

type TabKey = 'outputs' | 'pipelines' | 'operations';

const TAB_KEYS = ['outputs', 'pipelines', 'operations'] as const;

const tabs: { key: TabKey; label: string; icon: React.ElementType }[] = [
  { key: 'outputs', label: 'Outputs', icon: Database },
  { key: 'pipelines', label: 'Pipelines', icon: GitBranch },
  { key: 'operations', label: 'Operations', icon: Activity },
];

// Map reconciler statuses to the values understood by StatusBadge /
// statusBgColor in lib/utils.ts. `completed` → success (green),
// `failed`/`superseded` → error (red), `running` → progressing (blue with
// pulsing dot), `pending` → info.
function mapLoggingOperationStatus(s: string): string {
  switch (s) {
    case 'completed':
      return 'healthy';
    case 'running':
      return 'progressing';
    case 'pending':
      return 'pending';
    case 'failed':
    case 'superseded':
      return 'error';
    default:
      return 'unknown';
  }
}

function titleCaseStatus(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  return s.slice(0, max - 1) + '…';
}

const outputTypeIcons: Record<string, React.ElementType> = {
  elasticsearch: Database,
  loki: FileText,
  splunk: Cloud,
  cloudwatch: Cloud,
  datadog: Cloud,
  s3: HardDrive,
  syslog: Server,
};

const outputTypeFields: Record<
  LoggingOutputType,
  { label: string; fields: { key: string; label: string; type: string; placeholder: string }[] }
> = {
  elasticsearch: {
    label: 'Elasticsearch',
    fields: [
      { key: 'url', label: 'URL', type: 'text', placeholder: 'https://elasticsearch.example.com:9200' },
      { key: 'index', label: 'Index', type: 'text', placeholder: 'kubernetes-logs' },
      { key: 'username', label: 'Username', type: 'text', placeholder: 'elastic' },
      { key: 'password', label: 'Password', type: 'password', placeholder: 'Password' },
    ],
  },
  loki: {
    label: 'Loki',
    fields: [
      { key: 'url', label: 'URL', type: 'text', placeholder: 'https://loki.example.com:3100' },
      { key: 'tenant_id', label: 'Tenant ID', type: 'text', placeholder: 'default' },
      { key: 'labels', label: 'Labels', type: 'text', placeholder: 'job=kubernetes, env=production' },
    ],
  },
  splunk: {
    label: 'Splunk',
    fields: [
      { key: 'hec_url', label: 'HEC URL', type: 'text', placeholder: 'https://splunk.example.com:8088' },
      { key: 'token', label: 'HEC Token', type: 'password', placeholder: 'Token' },
      { key: 'index', label: 'Index', type: 'text', placeholder: 'main' },
      { key: 'source', label: 'Source', type: 'text', placeholder: 'kubernetes' },
    ],
  },
  cloudwatch: {
    label: 'CloudWatch',
    fields: [
      { key: 'region', label: 'Region', type: 'text', placeholder: 'us-east-1' },
      { key: 'log_group', label: 'Log Group', type: 'text', placeholder: '/kubernetes/cluster-logs' },
      { key: 'access_key', label: 'Access Key', type: 'text', placeholder: 'AKIA...' },
      { key: 'secret_key', label: 'Secret Key', type: 'password', placeholder: 'Secret key' },
    ],
  },
  datadog: {
    label: 'Datadog',
    fields: [
      { key: 'api_key', label: 'API Key', type: 'password', placeholder: 'Datadog API key' },
      { key: 'site', label: 'Site', type: 'text', placeholder: 'datadoghq.com' },
      { key: 'service', label: 'Service', type: 'text', placeholder: 'kubernetes' },
      { key: 'source', label: 'Source', type: 'text', placeholder: 'kubernetes' },
    ],
  },
  s3: {
    label: 'S3',
    fields: [
      { key: 'bucket', label: 'Bucket', type: 'text', placeholder: 'my-log-bucket' },
      { key: 'region', label: 'Region', type: 'text', placeholder: 'us-east-1' },
      { key: 'prefix', label: 'Prefix', type: 'text', placeholder: 'logs/' },
      { key: 'access_key', label: 'Access Key', type: 'text', placeholder: 'AKIA...' },
      { key: 'secret_key', label: 'Secret Key', type: 'password', placeholder: 'Secret key' },
    ],
  },
  syslog: {
    label: 'Syslog',
    fields: [
      { key: 'host', label: 'Host', type: 'text', placeholder: 'syslog.example.com' },
      { key: 'port', label: 'Port', type: 'text', placeholder: '514' },
      { key: 'protocol', label: 'Protocol', type: 'text', placeholder: 'tcp' },
      { key: 'facility', label: 'Facility', type: 'text', placeholder: 'local0' },
    ],
  },
};

export default function LoggingPage() {
  const queryClient = useQueryClient();
  const [activeTab, setActiveTab] = useTabParam(TAB_KEYS, 'outputs');
  const [showOutputModal, setShowOutputModal] = useState(false);
  const [showPipelineModal, setShowPipelineModal] = useState(false);
  const [opsStatusFilter, setOpsStatusFilter] = useState<string>('');
  const [opsTargetFilter, setOpsTargetFilter] = useState<string>('');

  const { data: outputs, isLoading: outputsLoading } = useLoggingOutputs();
  const { data: pipelines, isLoading: pipelinesLoading } = useLoggingPipelines();
  // Server-side params kept narrow so the list query key changes drive the
  // refetch — client-side filtering of the bigger fields happens in DataTable.
  const { data: operations, isLoading: operationsLoading } = useLoggingOperations({
    status: opsStatusFilter || undefined,
    target_type: opsTargetFilter || undefined,
    limit: 100,
  });
  const testOutput = useTestLoggingOutput();
  const retryOperation = useRetryLoggingOperation();

  const handleDeleteOutput = async (id: string) => {
    if (!confirm('Delete this logging output?')) return;
    try {
      await deleteLoggingOutput(id);
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess('Logging output deleted');
    } catch (error) {
      toastError(`Failed to delete output: ${error instanceof Error ? error.message : 'Unknown error'}`);
    }
  };

  const handleToggleOutput = async (output: LoggingOutput) => {
    try {
      await updateLoggingOutput(output.id, { enabled: !output.enabled });
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess(`Output ${output.enabled ? 'disabled' : 'enabled'}`);
    } catch (error) {
      toastError(`Failed to update output: ${error instanceof Error ? error.message : 'Unknown error'}`);
    }
  };

  const handleDeletePipeline = async (id: string) => {
    if (!confirm('Delete this logging pipeline?')) return;
    try {
      await deleteLoggingPipeline(id);
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess('Logging pipeline deleted');
    } catch (error) {
      toastError(`Failed to delete pipeline: ${error instanceof Error ? error.message : 'Unknown error'}`);
    }
  };

  const handleTogglePipeline = async (pipeline: LoggingPipeline) => {
    try {
      await updateLoggingPipeline(pipeline.id, { enabled: !pipeline.enabled });
      queryClient.invalidateQueries({ queryKey: queryKeys.logging.all });
      toastSuccess(`Pipeline ${pipeline.enabled ? 'disabled' : 'enabled'}`);
    } catch (error) {
      toastError(`Failed to update pipeline: ${error instanceof Error ? error.message : 'Unknown error'}`);
    }
  };

  const outputColumns: Column<LoggingOutput>[] = [
    {
      key: 'name',
      header: 'Output',
      accessor: (row) => {
        const TypeIcon = outputTypeIcons[row.type] || Database;
        return (
          <div className="flex items-center gap-2">
            <TypeIcon className="h-4 w-4 text-muted-foreground" />
            <div>
              <p className="font-medium text-foreground">{row.name}</p>
              <p className="text-xs text-muted-foreground capitalize">{row.type}</p>
            </div>
          </div>
        );
      },
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.type}
        </span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName || 'All'}</span>
      ),
    },
    {
      key: 'status',
      header: 'Connection',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'enabled',
      header: 'Enabled',
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            handleToggleOutput(row);
          }}
          className={cn(
            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
            row.enabled ? 'bg-primary' : 'bg-muted'
          )}
        >
          <span
            className={cn(
              'inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
              row.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'
            )}
          />
        </button>
      ),
      sortable: false,
    },
    {
      key: 'created',
      header: 'Created',
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
            onClick={() => testOutput.mutate(row.id)}
            disabled={testOutput.isPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Test Output"
          >
            <Send className="h-3 w-3" />
            Test
          </button>
          <button
            onClick={() => handleDeleteOutput(row.id)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete output"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  const operationColumns: Column<LoggingOperation>[] = [
    {
      key: 'targetType',
      header: 'Target Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.targetType}
        </span>
      ),
      sortAccessor: (row) => row.targetType,
    },
    {
      key: 'operation',
      header: 'Operation',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground capitalize">
          {row.operation}
        </span>
      ),
      sortAccessor: (row) => row.operation,
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={mapLoggingOperationStatus(row.status)}
          label={titleCaseStatus(row.status)}
          pulse={row.status === 'running'}
        />
      ),
      sortAccessor: (row) => row.status,
    },
    {
      key: 'created',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground" title={row.createdAt}>
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
      sortAccessor: (row) => row.createdAt,
    },
    {
      key: 'updated',
      header: 'Age / Updated',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground" title={row.updatedAt}>
          {formatRelativeTime(row.updatedAt)}
        </span>
      ),
      sortAccessor: (row) => row.updatedAt,
    },
    {
      key: 'error',
      header: 'Error',
      accessor: (row) =>
        row.errorMessage ? (
          <span
            className="text-xs text-status-error/80 line-clamp-1 max-w-[260px] block"
            title={row.errorMessage}
          >
            {truncate(row.errorMessage, 80)}
          </span>
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => {
        const retryable = row.status === 'failed' || row.status === 'superseded';
        if (!retryable) {
          return <span className="text-xs text-muted-foreground">—</span>;
        }
        return (
          <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
            <button
              onClick={() => retryOperation.mutate(row.id)}
              disabled={retryOperation.isPending}
              className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground
                hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
              title="Retry operation"
            >
              <RotateCcw className={cn('h-3 w-3', retryOperation.isPending && 'animate-spin')} />
              Retry
            </button>
          </div>
        );
      },
      sortable: false,
    },
  ];

  const pipelineColumns: Column<LoggingPipeline>[] = [
    {
      key: 'name',
      header: 'Pipeline',
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">{row.name}</p>
          {row.description && (
            <p className="text-xs text-muted-foreground truncate max-w-[300px]">{row.description}</p>
          )}
        </div>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName || 'All'}</span>
      ),
    },
    {
      key: 'namespaces',
      header: 'Namespaces',
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.namespaces.length === 0 ? (
            <span className="text-xs text-muted-foreground">All</span>
          ) : (
            row.namespaces.slice(0, 3).map((ns) => (
              <span key={ns} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
                {ns}
              </span>
            ))
          )}
          {row.namespaces.length > 3 && (
            <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
              +{row.namespaces.length - 3}
            </span>
          )}
        </div>
      ),
      sortable: false,
    },
    {
      key: 'outputs',
      header: 'Outputs',
      accessor: (row) => (
        <span className="tabular-nums text-sm">{row.outputNames.length}</span>
      ),
      sortAccessor: (row) => row.outputNames.length,
      align: 'center',
    },
    {
      key: 'enabled',
      header: 'Enabled',
      accessor: (row) => (
        <button
          onClick={(e) => {
            e.stopPropagation();
            handleTogglePipeline(row);
          }}
          className={cn(
            'relative inline-flex h-5 w-9 items-center rounded-full transition-colors',
            row.enabled ? 'bg-primary' : 'bg-muted'
          )}
        >
          <span
            className={cn(
              'inline-block h-3.5 w-3.5 transform rounded-full bg-white transition-transform',
              row.enabled ? 'translate-x-[18px]' : 'translate-x-[3px]'
            )}
          />
        </button>
      ),
      sortable: false,
    },
    {
      key: 'created',
      header: 'Created',
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
            onClick={() => handleDeletePipeline(row.id)}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete pipeline"
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
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Logging</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Logging outputs and pipeline configuration
          </p>
        </div>
        <div className="flex items-center gap-2">
          {activeTab === 'outputs' && (
            <button
              onClick={() => setShowOutputModal(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Output
            </button>
          )}
          {activeTab === 'pipelines' && (
            <button
              onClick={() => setShowPipelineModal(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Create Pipeline
            </button>
          )}
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
        {activeTab === 'outputs' && (
          <DataTable
            data={outputs || []}
            columns={outputColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search logging outputs..."
            loading={outputsLoading}
            emptyMessage="No logging outputs configured"
          />
        )}

        {activeTab === 'pipelines' && (
          <DataTable
            data={pipelines || []}
            columns={pipelineColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search logging pipelines..."
            loading={pipelinesLoading}
            emptyMessage="No logging pipelines configured"
          />
        )}

        {activeTab === 'operations' && (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center gap-2">
              <label className="text-xs text-muted-foreground">Status</label>
              <select
                value={opsStatusFilter}
                onChange={(e) => setOpsStatusFilter(e.target.value)}
                className="h-8 px-2 rounded-md border border-border bg-background text-xs
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="">All</option>
                <option value="pending">Pending</option>
                <option value="running">Running</option>
                <option value="completed">Completed</option>
                <option value="failed">Failed</option>
                <option value="superseded">Superseded</option>
              </select>
              <label className="text-xs text-muted-foreground ml-2">Target</label>
              <select
                value={opsTargetFilter}
                onChange={(e) => setOpsTargetFilter(e.target.value)}
                className="h-8 px-2 rounded-md border border-border bg-background text-xs
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="">All</option>
                <option value="output">Output</option>
                <option value="pipeline">Pipeline</option>
              </select>
              {(opsStatusFilter || opsTargetFilter) && (
                <button
                  onClick={() => {
                    setOpsStatusFilter('');
                    setOpsTargetFilter('');
                  }}
                  className="inline-flex items-center gap-1 h-8 px-2 rounded-md text-xs text-muted-foreground
                    hover:text-foreground hover:bg-accent transition-colors"
                >
                  <X className="h-3 w-3" /> Clear
                </button>
              )}
            </div>
            <DataTable
              data={operations || []}
              columns={operationColumns}
              keyExtractor={(row) => row.id}
              searchPlaceholder="Search operations..."
              loading={operationsLoading}
              emptyMessage="No reconciler activity yet."
              pageSize={20}
            />
          </div>
        )}
      </div>

      {/* Create Output Modal */}
      {showOutputModal && (
        <CreateOutputModal onClose={() => setShowOutputModal(false)} />
      )}

      {/* Create Pipeline Modal */}
      {showPipelineModal && (
        <CreatePipelineModal
          outputs={outputs || []}
          onClose={() => setShowPipelineModal(false)}
        />
      )}
    </div>
  );
}

// ============================================================
// Create Output Modal
// ============================================================

function CreateOutputModal({ onClose }: { onClose: () => void }) {
  const createOutput = useCreateLoggingOutput();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];

  const [form, setForm] = useState({
    name: '',
    type: 'elasticsearch' as LoggingOutputType,
    clusterId: '',
    enabled: true,
    config: {} as Record<string, string>,
  });

  const typeConfig = outputTypeFields[form.type];

  const handleSave = async () => {
    if (!form.name) {
      toastError('Name is required');
      return;
    }

    try {
      await createOutput.mutateAsync({
        name: form.name,
        type: form.type,
        clusterId: form.clusterId || undefined,
        enabled: form.enabled,
        config: form.config,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create Logging Output</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Name</label>
            <input
              type="text"
              value={form.name}
              onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
              placeholder="Production Elasticsearch"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Type</label>
            <div className="flex flex-wrap gap-1.5">
              {(Object.keys(outputTypeFields) as LoggingOutputType[]).map((type) => (
                <button
                  key={type}
                  onClick={() => setForm((f) => ({ ...f, type, config: {} }))}
                  className={cn(
                    'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                    form.type === type
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {outputTypeFields[type].label}
                </button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Cluster (optional)</label>
            <select
              value={form.clusterId}
              onChange={(e) => setForm((f) => ({ ...f, clusterId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">All Clusters</option>
              {clusters.map((cluster) => (
                <option key={cluster.id} value={cluster.id}>
                  {cluster.displayName}
                </option>
              ))}
            </select>
          </div>

          {/* Type-specific fields */}
          {typeConfig.fields.map((field) => (
            <div key={field.key} className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">{field.label}</label>
              <input
                type={field.type}
                value={form.config[field.key] || ''}
                onChange={(e) =>
                  setForm((f) => ({
                    ...f,
                    config: { ...f.config, [field.key]: e.target.value },
                  }))
                }
                placeholder={field.placeholder}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
          ))}

          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
              className="rounded border-border text-primary focus:ring-ring"
            />
            <span className="text-sm text-foreground">Enabled</span>
          </label>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createOutput.isPending || !form.name}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createOutput.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create Output
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}

// ============================================================
// Create Pipeline Modal
// ============================================================

function CreatePipelineModal({
  outputs,
  onClose,
}: {
  outputs: LoggingOutput[];
  onClose: () => void;
}) {
  const createPipeline = useCreateLoggingPipeline();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const clusters = clustersData?.data || [];

  const [form, setForm] = useState({
    name: '',
    description: '',
    clusterId: '',
    namespaces: [] as string[],
    outputIds: [] as string[],
    labelKey: '',
    labelValue: '',
    labels: {} as Record<string, string>,
    enabled: true,
  });

  const { data: namespacesData } = useClusterNamespaces(form.clusterId);
  const namespaces = namespacesData || [];

  const toggleNamespace = (ns: string) => {
    setForm((f) => ({
      ...f,
      namespaces: f.namespaces.includes(ns)
        ? f.namespaces.filter((n) => n !== ns)
        : [...f.namespaces, ns],
    }));
  };

  const toggleOutput = (id: string) => {
    setForm((f) => ({
      ...f,
      outputIds: f.outputIds.includes(id)
        ? f.outputIds.filter((o) => o !== id)
        : [...f.outputIds, id],
    }));
  };

  const addLabel = () => {
    if (form.labelKey && form.labelValue) {
      setForm((f) => ({
        ...f,
        labels: { ...f.labels, [f.labelKey]: f.labelValue },
        labelKey: '',
        labelValue: '',
      }));
    }
  };

  const removeLabel = (key: string) => {
    setForm((f) => {
      const labels = { ...f.labels };
      delete labels[key];
      return { ...f, labels };
    });
  };

  const handleSave = async () => {
    if (!form.name) {
      toastError('Name is required');
      return;
    }
    if (form.outputIds.length === 0) {
      toastError('Select at least one output');
      return;
    }

    const filters = Object.entries(form.labels).map(([field, pattern]) => ({
      type: 'include' as const,
      field,
      pattern,
    }));

    try {
      await createPipeline.mutateAsync({
        name: form.name,
        description: form.description || undefined,
        clusterId: form.clusterId || undefined,
        namespaces: form.namespaces,
        outputIds: form.outputIds,
        filters,
        enabled: form.enabled,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-2xl max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Create Logging Pipeline</h3>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Name</label>
              <input
                type="text"
                value={form.name}
                onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                placeholder="Production Log Pipeline"
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
            </div>
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Cluster</label>
              <select
                value={form.clusterId}
                onChange={(e) => setForm((f) => ({ ...f, clusterId: e.target.value, namespaces: [] }))}
                className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                <option value="">All Clusters</option>
                {clusters.map((cluster) => (
                  <option key={cluster.id} value={cluster.id}>
                    {cluster.displayName}
                  </option>
                ))}
              </select>
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Describe this pipeline's purpose"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          {/* Namespaces */}
          {form.clusterId && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Namespaces</label>
              <div className="flex flex-wrap gap-1.5 max-h-32 overflow-y-auto p-2 rounded-md border border-border bg-background">
                {namespaces.length === 0 ? (
                  <span className="text-xs text-muted-foreground">No namespaces found</span>
                ) : (
                  namespaces.map((ns) => (
                    <button
                      key={ns.name}
                      onClick={() => toggleNamespace(ns.name)}
                      className={cn(
                        'px-2.5 py-1 rounded text-xs font-medium transition-colors',
                        form.namespaces.includes(ns.name)
                          ? 'bg-primary text-primary-foreground'
                          : 'bg-muted text-muted-foreground hover:text-foreground'
                      )}
                    >
                      {ns.name}
                    </button>
                  ))
                )}
              </div>
              {form.namespaces.length === 0 && (
                <p className="text-xs text-muted-foreground">No namespaces selected (will collect from all)</p>
              )}
            </div>
          )}

          {/* Label selectors */}
          <div className="space-y-2">
            <label className="text-sm font-medium text-foreground">Label Selectors</label>
            <div className="flex gap-2">
              <input
                type="text"
                value={form.labelKey}
                onChange={(e) => setForm((f) => ({ ...f, labelKey: e.target.value }))}
                placeholder="Label key"
                className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-xs font-mono
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <input
                type="text"
                value={form.labelValue}
                onChange={(e) => setForm((f) => ({ ...f, labelValue: e.target.value }))}
                placeholder="Value"
                className="flex-1 h-8 px-2.5 rounded border border-border bg-background text-xs font-mono
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              <button
                onClick={addLabel}
                disabled={!form.labelKey || !form.labelValue}
                className="h-8 px-2.5 rounded border border-border text-xs text-muted-foreground
                  hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
              >
                <Plus className="h-3.5 w-3.5" />
              </button>
            </div>
            {Object.entries(form.labels).length > 0 && (
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(form.labels).map(([k, v]) => (
                  <span
                    key={k}
                    className="inline-flex items-center gap-1 text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono"
                  >
                    {k}={v}
                    <button onClick={() => removeLabel(k)} className="hover:text-foreground">
                      <X className="h-3 w-3" />
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>

          {/* Outputs */}
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Outputs</label>
            <div className="space-y-1.5 max-h-40 overflow-y-auto p-2 rounded-md border border-border bg-background">
              {outputs.length === 0 ? (
                <span className="text-xs text-muted-foreground">No outputs available. Create an output first.</span>
              ) : (
                outputs.map((output) => (
                  <label
                    key={output.id}
                    className="flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent cursor-pointer"
                  >
                    <input
                      type="checkbox"
                      checked={form.outputIds.includes(output.id)}
                      onChange={() => toggleOutput(output.id)}
                      className="rounded border-border text-primary focus:ring-ring"
                    />
                    <span className="text-foreground">{output.name}</span>
                    <span className="text-xs text-muted-foreground capitalize">({output.type})</span>
                  </label>
                ))
              )}
            </div>
          </div>

          <label className="flex items-center gap-2 cursor-pointer">
            <input
              type="checkbox"
              checked={form.enabled}
              onChange={(e) => setForm((f) => ({ ...f, enabled: e.target.checked }))}
              className="rounded border-border text-primary focus:ring-ring"
            />
            <span className="text-sm text-foreground">Enabled</span>
          </label>
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={handleSave}
            disabled={createPipeline.isPending || !form.name || form.outputIds.length === 0}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createPipeline.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create Pipeline
          </button>
        </div>
      </div>
    </OverlayShell>
  );
}
