'use client';

import { useEffect, useMemo, useState } from 'react';
import { useSearchParams } from 'next/navigation';
import {
  useHelmRepositories,
  useCreateHelmRepository,
  useSyncHelmRepository,
  useDeleteHelmRepository,
  useHelmCharts,
  useHelmChartVersions,
  useInstalledCharts,
  useInstallHelmChart,
  useUpgradeInstalledChart,
  useUninstallChart,
  useRollbackChart,
  useClusters,
} from '@/lib/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { HelmValuesForm } from '@/components/catalog/helm-values-form';
import { SuggestedCatalogs } from '@/components/catalog/suggested-catalogs';
import { StatusBadge } from '@/components/ui/status-badge';
import { formatRelativeTime, cn } from '@/lib/utils';
import {
  dumpHelmValuesYAML,
  hasRenderableSchema,
  mergeSchemaDefaults,
  parseHelmValuesYAML,
  type HelmValuesObject,
  type HelmValuesSchemaNode,
} from '@/lib/helm-values-schema';
import type {
  HelmRepository,
  HelmChart,
  HelmChartVersion,
  HelmChartCategory,
  InstalledChart,
  HelmRepoType,
} from '@/types';
import {
  Package,
  Plus,
  Search,
  RefreshCw,
  Trash2,
  X,
  Loader2,
  Download,
  RotateCcw,
  ArrowUpCircle,
  ExternalLink,
  Grid3X3,
  List,
  Globe,
  ChevronDown,
  Braces,
  FileCode2,
  AlertTriangle,
} from 'lucide-react';

type TabKey = 'browse' | 'installed' | 'repositories';

const tabs: { key: TabKey; label: string }[] = [
  { key: 'browse', label: 'Browse Charts' },
  { key: 'installed', label: 'Installed' },
  { key: 'repositories', label: 'Repositories' },
];

const categories: { key: HelmChartCategory | 'all'; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'monitoring', label: 'Monitoring' },
  { key: 'logging', label: 'Logging' },
  { key: 'security', label: 'Security' },
  { key: 'database', label: 'Database' },
  { key: 'networking', label: 'Networking' },
  { key: 'storage', label: 'Storage' },
  { key: 'messaging', label: 'Messaging' },
  { key: 'ci-cd', label: 'CI/CD' },
  { key: 'other', label: 'Other' },
];

const categoryColors: Record<string, string> = {
  monitoring: 'bg-blue-500/10 text-blue-500',
  logging: 'bg-green-500/10 text-green-500',
  security: 'bg-red-500/10 text-red-500',
  database: 'bg-purple-500/10 text-purple-500',
  networking: 'bg-orange-500/10 text-orange-500',
  storage: 'bg-cyan-500/10 text-cyan-500',
  messaging: 'bg-yellow-500/10 text-yellow-500',
  'ci-cd': 'bg-indigo-500/10 text-indigo-500',
  other: 'bg-muted text-muted-foreground',
};

export default function CatalogPage() {
  const [activeTab, setActiveTab] = useState<TabKey>('browse');
  const [selectedCategory, setSelectedCategory] = useState<HelmChartCategory | 'all'>('all');
  const initialSearchParams = useSearchParams();
  const [searchQuery, setSearchQuery] = useState(initialSearchParams?.get('search') ?? '');
  const presetClusterIdPage = initialSearchParams?.get('cluster_id') ?? '';
  const [selectedChart, setSelectedChart] = useState<HelmChart | null>(null);
  const [showRepoModal, setShowRepoModal] = useState(false);
  const [showInstallModal, setShowInstallModal] = useState(false);
  const [installChart, setInstallChart] = useState<{ chart: HelmChart; version: HelmChartVersion } | null>(null);

  const { data: charts, isLoading: chartsLoading } = useHelmCharts({
    category: selectedCategory !== 'all' ? selectedCategory : undefined,
    search: searchQuery || undefined,
  });
  const { data: installed, isLoading: installedLoading } = useInstalledCharts();
  const { data: repos, isLoading: reposLoading } = useHelmRepositories();
  const { data: presetClusterData } = useClusters({ pageSize: 100 });
  const presetCluster = useMemo(
    () => (presetClusterIdPage ? (presetClusterData?.data || []).find((c) => c.id === presetClusterIdPage) : undefined),
    [presetClusterIdPage, presetClusterData]
  );

  const syncRepo = useSyncHelmRepository();
  const deleteRepo = useDeleteHelmRepository();
  const uninstall = useUninstallChart();
  const rollback = useRollbackChart();

  // --- Installed Charts Table ---
  const installedColumns: Column<InstalledChart>[] = [
    {
      key: 'release',
      header: 'Release',
      accessor: (row) => (
        <span className="font-medium text-foreground font-mono text-xs">{row.releaseName}</span>
      ),
    },
    {
      key: 'chart',
      header: 'Chart',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.chartName}</span>
      ),
    },
    {
      key: 'version',
      header: 'Version',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground font-mono">
          {row.chartVersionLabel}
        </span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.clusterName}</span>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">{row.namespace}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: 'revision',
      header: 'Rev',
      accessor: (row) => (
        <span className="tabular-nums text-xs text-muted-foreground">{row.revision}</span>
      ),
      sortAccessor: (row) => row.revision,
      align: 'center',
    },
    {
      key: 'installedBy',
      header: 'Installed By',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">{row.installedBy}</span>
      ),
    },
    {
      key: 'date',
      header: 'Date',
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
              /* upgrade would open a modal - for now, just a placeholder action */
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            title="Upgrade"
          >
            <ArrowUpCircle className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => {
              if (row.revision > 1) {
                rollback.mutate({ id: row.id, revision: row.revision - 1 });
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Rollback"
            disabled={row.revision <= 1}
          >
            <RotateCcw className="h-3.5 w-3.5" />
          </button>
          <button
            onClick={() => {
              if (confirm(`Uninstall release "${row.releaseName}"?`)) {
                uninstall.mutate(row.id);
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Uninstall"
          >
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ),
      sortable: false,
    },
  ];

  // --- Repository Table ---
  const repoColumns: Column<HelmRepository>[] = [
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Globe className="h-4 w-4 text-muted-foreground" />
          <span className="font-medium text-foreground">{row.name}</span>
          {row.isDefault && (
            <span className="text-2xs px-1.5 py-0.5 rounded bg-primary/10 text-primary font-medium">Default</span>
          )}
        </div>
      ),
    },
    {
      key: 'url',
      header: 'URL',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground truncate max-w-[300px] block">{row.url}</span>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground uppercase">
          {row.repoType}
        </span>
      ),
    },
    {
      key: 'charts',
      header: 'Charts',
      accessor: (row) => (
        <span className="tabular-nums text-sm text-muted-foreground">{row.chartCount}</span>
      ),
      sortAccessor: (row) => row.chartCount,
      align: 'center',
    },
    {
      key: 'lastSynced',
      header: 'Last Synced',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastSyncedAt ? formatRelativeTime(row.lastSyncedAt) : 'Never'}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) => (
        <StatusBadge
          status={row.enabled ? 'active' : 'disconnected'}
          label={row.enabled ? 'Enabled' : 'Disabled'}
        />
      ),
    },
    {
      key: 'actions',
      header: '',
      accessor: (row) => (
        <div className="flex items-center gap-1" onClick={(e) => e.stopPropagation()}>
          <button
            onClick={() => syncRepo.mutate(row.id)}
            disabled={syncRepo.isPending}
            className="inline-flex items-center gap-1 px-2 py-1 rounded text-xs text-muted-foreground
              hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
            title="Sync repository"
          >
            <RefreshCw className={cn('h-3 w-3', syncRepo.isPending && 'animate-spin')} />
            Sync
          </button>
          <button
            onClick={() => {
              if (confirm(`Delete repository "${row.name}"?`)) {
                deleteRepo.mutate(row.id);
              }
            }}
            className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
            title="Delete repository"
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
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Catalog</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Browse, install, and manage Helm charts across your clusters
          </p>
          {presetClusterIdPage && (
            <div className="mt-2 inline-flex items-center gap-2 text-xs px-2 py-1 rounded bg-accent/40 text-foreground">
              <Package className="h-3.5 w-3.5" />
              Installing onto{' '}
              <span className="font-medium">
                {presetCluster?.displayName || presetCluster?.name || presetClusterIdPage}
              </span>
            </div>
          )}
        </div>
        <div className="flex items-center gap-2">
          {activeTab === 'repositories' && (
            <button
              onClick={() => setShowRepoModal(true)}
              className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity"
            >
              <Plus className="h-4 w-4" />
              Add Repository
            </button>
          )}
        </div>
      </div>

      {/* Tabs */}
      <div className="border-b border-border">
        <nav className="flex gap-6">
          {tabs.map((tab) => (
            <button
              key={tab.key}
              onClick={() => setActiveTab(tab.key)}
              className={cn(
                'pb-3 text-sm font-medium border-b-2 transition-colors',
                activeTab === tab.key
                  ? 'border-foreground text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              )}
            >
              {tab.label}
              {tab.key === 'installed' && installed && (
                <span className="ml-1.5 text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground tabular-nums">
                  {installed.length}
                </span>
              )}
              {tab.key === 'repositories' && repos && (
                <span className="ml-1.5 text-xs px-1.5 py-0.5 rounded-full bg-muted text-muted-foreground tabular-nums">
                  {repos.length}
                </span>
              )}
            </button>
          ))}
        </nav>
      </div>

      {/* Content */}
      <div className="animate-fade-in">
        {/* Browse Charts Tab */}
        {activeTab === 'browse' && (
          <div className="space-y-4">
            {/* Search & Category Filter */}
            <div className="flex items-center gap-3">
              <div className="relative max-w-sm flex-1">
                <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
                <input
                  type="text"
                  placeholder="Search charts..."
                  value={searchQuery}
                  onChange={(e) => setSearchQuery(e.target.value)}
                  className="w-full h-9 pl-9 pr-8 rounded-md border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                />
                {searchQuery && (
                  <button
                    onClick={() => setSearchQuery('')}
                    className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                )}
              </div>
            </div>

            {/* Category Tabs */}
            <div className="flex flex-wrap gap-1.5">
              {categories.map((cat) => (
                <button
                  key={cat.key}
                  onClick={() => setSelectedCategory(cat.key)}
                  className={cn(
                    'px-3 py-1.5 rounded-md text-xs font-medium transition-colors',
                    selectedCategory === cat.key
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {cat.label}
                </button>
              ))}
            </div>

            {/* Chart Grid */}
            {chartsLoading ? (
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                {Array.from({ length: 8 }).map((_, i) => (
                  <div key={i} className="rounded-lg border border-border p-4 space-y-3">
                    <div className="flex items-center gap-3">
                      <div className="h-10 w-10 rounded-lg bg-muted animate-pulse" />
                      <div className="flex-1 space-y-1.5">
                        <div className="h-4 w-24 rounded bg-muted animate-pulse" />
                        <div className="h-3 w-16 rounded bg-muted animate-pulse" />
                      </div>
                    </div>
                    <div className="h-3 w-full rounded bg-muted animate-pulse" />
                    <div className="h-3 w-3/4 rounded bg-muted animate-pulse" />
                  </div>
                ))}
              </div>
            ) : (charts || []).length === 0 ? (
              <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
                <Package className="h-10 w-10 mb-3" />
                <p className="text-sm">No charts found</p>
                <p className="text-xs mt-1">Try adjusting your search or category filter</p>
              </div>
            ) : (
              <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
                {(charts || []).map((chart) => (
                  <button
                    key={chart.id}
                    onClick={() => setSelectedChart(chart)}
                    className="rounded-lg border border-border p-4 text-left hover:border-foreground/20 hover:bg-muted/30
                      transition-colors group"
                  >
                    <div className="flex items-start gap-3">
                      <div className="flex-shrink-0 h-10 w-10 rounded-lg bg-muted/60 flex items-center justify-center overflow-hidden">
                        {chart.iconUrl ? (
                          <img src={chart.iconUrl} alt={chart.displayName} className="h-8 w-8 object-contain" />
                        ) : (
                          <Package className="h-5 w-5 text-muted-foreground" />
                        )}
                      </div>
                      <div className="flex-1 min-w-0">
                        <p className="font-medium text-foreground text-sm truncate group-hover:text-primary transition-colors">
                          {chart.displayName || chart.name}
                        </p>
                        <p className="text-xs text-muted-foreground truncate">{chart.repositoryName}</p>
                      </div>
                    </div>
                    <p className="text-xs text-muted-foreground mt-2 line-clamp-2 min-h-[2rem]">
                      {chart.description || 'No description available'}
                    </p>
                    <div className="flex items-center justify-between mt-3">
                      <span className={cn('text-2xs px-1.5 py-0.5 rounded font-medium', categoryColors[chart.category] || categoryColors.other)}>
                        {chart.category}
                      </span>
                      <span className="text-xs font-mono text-muted-foreground">v{chart.latestVersion}</span>
                    </div>
                  </button>
                ))}
              </div>
            )}
          </div>
        )}

        {/* Installed Tab */}
        {activeTab === 'installed' && (
          <DataTable
            data={installed || []}
            columns={installedColumns}
            keyExtractor={(row) => row.id}
            searchPlaceholder="Search installed releases..."
            loading={installedLoading}
            emptyMessage="No charts installed"
          />
        )}

        {/* Repositories Tab */}
        {activeTab === 'repositories' && (
          <div className="space-y-6">
            <SuggestedCatalogs existing={repos} />
            <div className="space-y-3">
              <h2 className="text-sm font-semibold text-foreground">Your repositories</h2>
              <DataTable
                data={repos || []}
                columns={repoColumns}
                keyExtractor={(row) => row.id}
                searchPlaceholder="Search repositories..."
                loading={reposLoading}
                emptyMessage="No repositories configured"
              />
            </div>
          </div>
        )}
      </div>

      {/* Chart Detail Modal */}
      {selectedChart && (
        <ChartDetailModal
          chart={selectedChart}
          onClose={() => setSelectedChart(null)}
          onInstall={(chart, version) => {
            setInstallChart({ chart, version });
            setShowInstallModal(true);
            setSelectedChart(null);
          }}
        />
      )}

      {/* Install Modal */}
      {showInstallModal && installChart && (
        <InstallChartModal
          chart={installChart.chart}
          version={installChart.version}
          onClose={() => {
            setShowInstallModal(false);
            setInstallChart(null);
          }}
        />
      )}

      {/* Add Repository Modal */}
      {showRepoModal && (
        <AddRepositoryModal onClose={() => setShowRepoModal(false)} />
      )}
    </div>
  );
}

// ============================================================
// Chart Detail Modal
// ============================================================

function ChartDetailModal({
  chart,
  onClose,
  onInstall,
}: {
  chart: HelmChart;
  onClose: () => void;
  onInstall: (chart: HelmChart, version: HelmChartVersion) => void;
}) {
  const { data: versions, isLoading: versionsLoading } = useHelmChartVersions(chart.id);
  const [selectedVersionId, setSelectedVersionId] = useState<string>('');

  const selectedVersion = versions?.find((v) => v.id === selectedVersionId) || versions?.[0];

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-2xl max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div className="flex items-center gap-3">
            <div className="h-10 w-10 rounded-lg bg-muted/60 flex items-center justify-center overflow-hidden">
              {chart.iconUrl ? (
                <img src={chart.iconUrl} alt={chart.displayName} className="h-8 w-8 object-contain" />
              ) : (
                <Package className="h-5 w-5 text-muted-foreground" />
              )}
            </div>
            <div>
              <h3 className="text-lg font-semibold text-foreground">{chart.displayName || chart.name}</h3>
              <p className="text-xs text-muted-foreground">{chart.repositoryName}</p>
            </div>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          {/* Chart info */}
          <div className="flex items-center gap-3 flex-wrap">
            <span className={cn('text-xs px-2 py-0.5 rounded font-medium', categoryColors[chart.category] || categoryColors.other)}>
              {chart.category}
            </span>
            {chart.keywords.map((kw) => (
              <span key={kw} className="text-xs px-2 py-0.5 rounded bg-muted text-muted-foreground">
                {kw}
              </span>
            ))}
          </div>

          <p className="text-sm text-muted-foreground">{chart.description}</p>

          {/* Version Selector */}
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Version</label>
            {versionsLoading ? (
              <div className="h-9 w-48 rounded-md bg-muted animate-pulse" />
            ) : (
              <select
                value={selectedVersionId || versions?.[0]?.id || ''}
                onChange={(e) => setSelectedVersionId(e.target.value)}
                className="w-48 h-9 px-3 rounded-md border border-border bg-background text-sm
                  focus:outline-none focus:ring-1 focus:ring-ring"
              >
                {(versions || []).map((v) => (
                  <option key={v.id} value={v.id}>
                    {v.version} (App: {v.appVersion})
                  </option>
                ))}
              </select>
            )}
          </div>

          {/* README */}
          {selectedVersion?.readme && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">README</label>
              <div className="rounded-lg border border-border bg-muted/30 p-4 max-h-64 overflow-y-auto">
                <pre className="text-xs text-muted-foreground whitespace-pre-wrap font-mono">
                  {selectedVersion.readme}
                </pre>
              </div>
            </div>
          )}

          {/* Default Values */}
          {selectedVersion?.defaultValues && (
            <div className="space-y-1.5">
              <label className="text-sm font-medium text-foreground">Default Values</label>
              <div className="rounded-lg border border-border bg-muted/30 p-4 max-h-48 overflow-y-auto">
                <pre className="text-xs text-muted-foreground whitespace-pre-wrap font-mono">
                  {selectedVersion.defaultValues}
                </pre>
              </div>
            </div>
          )}
        </div>

        <div className="flex items-center justify-end gap-2 px-6 py-4 border-t border-border flex-shrink-0 bg-muted/30">
          <button
            onClick={onClose}
            className="h-9 px-4 rounded-lg border border-border text-sm font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Close
          </button>
          <button
            onClick={() => {
              if (selectedVersion) {
                onInstall(chart, selectedVersion);
              }
            }}
            disabled={!selectedVersion}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            <Download className="h-4 w-4" />
            Install
          </button>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// Install Chart Modal
// ============================================================

function InstallChartModal({
  chart,
  version,
  onClose,
}: {
  chart: HelmChart;
  version: HelmChartVersion;
  onClose: () => void;
}) {
  const installChart = useInstallHelmChart();
  const { data: clustersData } = useClusters({ pageSize: 100 });
  const clusters = clustersData?.data || [];
  const schema = useMemo(
    () => (hasRenderableSchema(version.valuesSchema) ? (version.valuesSchema as HelmValuesSchemaNode) : null),
    [version.valuesSchema]
  );

  // Sprint 23: when arriving from an empty-state CTA on a cluster
  // detail page (e.g. "Install trivy-operator from Image Scans"), the
  // URL carries ?cluster_id=<uuid>. Pre-populate the target dropdown so
  // the operator doesn't have to pick again. Empty when absent.
  const searchParams = useSearchParams();
  const presetClusterId = searchParams?.get('cluster_id') ?? '';

  const [form, setForm] = useState({
    clusterId: presetClusterId,
    releaseName: chart.name,
    namespace: 'default',
    valuesOverride: version.defaultValues || '',
  });
  const [editorMode, setEditorMode] = useState<'form' | 'yaml'>(schema ? 'form' : 'yaml');
  const [yamlError, setYamlError] = useState<string | null>(null);
  const [schemaValues, setSchemaValues] = useState<HelmValuesObject>(() => {
    const parsed = parseHelmValuesYAML(version.defaultValues || '') || {};
    return (schema ? mergeSchemaDefaults(schema, parsed) : parsed) as HelmValuesObject;
  });

  useEffect(() => {
    const parsed = parseHelmValuesYAML(version.defaultValues || '') || {};
    setForm((current) => ({
      ...current,
      valuesOverride: version.defaultValues || '',
    }));
    setSchemaValues((schema ? mergeSchemaDefaults(schema, parsed) : parsed) as HelmValuesObject);
    setEditorMode(schema ? 'form' : 'yaml');
    setYamlError(null);
  }, [schema, version.defaultValues]);

  const handleInstall = async () => {
    try {
      await installChart.mutateAsync({
        cluster_id: form.clusterId,
        chart_version_id: version.id,
        release_name: form.releaseName,
        namespace: form.namespace,
        values_override: form.valuesOverride || undefined,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  const handleSchemaValuesChange = (next: HelmValuesObject) => {
    setSchemaValues(next);
    setForm((current) => ({
      ...current,
      valuesOverride: dumpHelmValuesYAML(next),
    }));
    setYamlError(null);
  };

  const handleYAMLChange = (nextYAML: string) => {
    setForm((current) => ({ ...current, valuesOverride: nextYAML }));
    if (!schema) return;
    const parsed = parseHelmValuesYAML(nextYAML);
    if (parsed == null) {
      setYamlError('YAML must parse to an object before the form can stay in sync.');
      return;
    }
    setSchemaValues(mergeSchemaDefaults(schema, parsed) as HelmValuesObject);
    setYamlError(null);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div>
            <h3 className="text-lg font-semibold text-foreground">
              Install {chart.displayName || chart.name}
            </h3>
            <p className="text-xs text-muted-foreground mt-0.5">Version {version.version}</p>
          </div>
          <button onClick={onClose} className="text-muted-foreground hover:text-foreground transition-colors">
            <X className="h-5 w-5" />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto p-6 space-y-4">
          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Target Cluster</label>
            <select
              value={form.clusterId}
              onChange={(e) => setForm((f) => ({ ...f, clusterId: e.target.value }))}
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option value="">Select a cluster...</option>
              {clusters.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.displayName} ({c.name})
                </option>
              ))}
            </select>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Release Name</label>
            <input
              type="text"
              value={form.releaseName}
              onChange={(e) => setForm((f) => ({ ...f, releaseName: e.target.value }))}
              placeholder="my-release"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Namespace</label>
            <input
              type="text"
              value={form.namespace}
              onChange={(e) => setForm((f) => ({ ...f, namespace: e.target.value }))}
              placeholder="default"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <label className="text-sm font-medium text-foreground">Values Override</label>
                <p className="text-xs text-muted-foreground mt-1">
                  {schema ? 'Edit with the chart schema form or switch to raw YAML.' : 'Raw YAML editor for chart values.'}
                </p>
              </div>
              {schema && (
                <div className="inline-flex rounded-md border border-border bg-muted/30 p-1">
                  <button
                    type="button"
                    onClick={() => setEditorMode('form')}
                    className={cn(
                      'inline-flex items-center gap-1 rounded px-2.5 py-1 text-xs font-medium transition-colors',
                      editorMode === 'form' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    <Braces className="h-3.5 w-3.5" />
                    Form
                  </button>
                  <button
                    type="button"
                    onClick={() => setEditorMode('yaml')}
                    className={cn(
                      'inline-flex items-center gap-1 rounded px-2.5 py-1 text-xs font-medium transition-colors',
                      editorMode === 'yaml' ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
                    )}
                  >
                    <FileCode2 className="h-3.5 w-3.5" />
                    YAML
                  </button>
                </div>
              )}
            </div>

            {schema && editorMode === 'form' ? (
              <div className="rounded-lg border border-border bg-muted/20 p-4">
                <HelmValuesForm schema={schema} value={schemaValues} onChange={handleSchemaValuesChange} />
              </div>
            ) : (
              <div className="space-y-2">
                <textarea
                  value={form.valuesOverride}
                  onChange={(e) => handleYAMLChange(e.target.value)}
                  placeholder="# Override default values here..."
                  rows={12}
                  className="w-full px-3 py-2 rounded-md border border-border bg-background text-sm font-mono
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring resize-none"
                />
                {yamlError && (
                  <div className="inline-flex items-center gap-2 rounded-md border border-status-warning/30 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
                    <AlertTriangle className="h-3.5 w-3.5" />
                    {yamlError}
                  </div>
                )}
              </div>
            )}
          </div>
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
            onClick={handleInstall}
            disabled={installChart.isPending || !form.clusterId || !form.releaseName || !form.namespace}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {installChart.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Install Chart
          </button>
        </div>
      </div>
    </div>
  );
}

// ============================================================
// Add Repository Modal
// ============================================================

function AddRepositoryModal({ onClose }: { onClose: () => void }) {
  const createRepo = useCreateHelmRepository();
  const [form, setForm] = useState({
    name: '',
    url: '',
    repoType: 'helm' as HelmRepoType,
    description: '',
    username: '',
    password: '',
  });
  const [showAuth, setShowAuth] = useState(false);

  const handleSave = async () => {
    try {
      await createRepo.mutateAsync({
        name: form.name,
        url: form.url,
        repoType: form.repoType,
        description: form.description || undefined,
        username: form.username || undefined,
        password: form.password || undefined,
      });
      onClose();
    } catch {
      // Error handled by mutation
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center">
      <div className="fixed inset-0 bg-black/60 backdrop-blur-sm" onClick={onClose} />
      <div className="relative w-full max-w-lg max-h-[85vh] rounded-xl border border-border bg-popover shadow-2xl flex flex-col">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <h3 className="text-lg font-semibold text-foreground">Add Repository</h3>
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
              placeholder="prometheus-community"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">URL</label>
            <input
              type="text"
              value={form.url}
              onChange={(e) => setForm((f) => ({ ...f, url: e.target.value }))}
              placeholder="https://prometheus-community.github.io/helm-charts"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Type</label>
            <div className="flex gap-1.5">
              {(['helm', 'oci'] as const).map((type) => (
                <button
                  key={type}
                  onClick={() => setForm((f) => ({ ...f, repoType: type }))}
                  className={cn(
                    'px-4 py-1.5 rounded-md text-xs font-medium transition-colors uppercase',
                    form.repoType === type
                      ? 'bg-primary text-primary-foreground'
                      : 'bg-muted text-muted-foreground hover:text-foreground'
                  )}
                >
                  {type}
                </button>
              ))}
            </div>
          </div>

          <div className="space-y-1.5">
            <label className="text-sm font-medium text-foreground">Description</label>
            <input
              type="text"
              value={form.description}
              onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
              placeholder="Optional description"
              className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
          </div>

          <button
            onClick={() => setShowAuth(!showAuth)}
            className="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            <ChevronDown className={cn('h-4 w-4 transition-transform', showAuth && 'rotate-180')} />
            Authentication (optional)
          </button>

          {showAuth && (
            <div className="space-y-4 pl-4 border-l-2 border-border">
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Username</label>
                <input
                  type="text"
                  value={form.username}
                  onChange={(e) => setForm((f) => ({ ...f, username: e.target.value }))}
                  placeholder="Username"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                />
              </div>
              <div className="space-y-1.5">
                <label className="text-sm font-medium text-foreground">Password</label>
                <input
                  type="password"
                  value={form.password}
                  onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                  placeholder="Password or token"
                  className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
                    placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
                />
              </div>
            </div>
          )}
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
            disabled={createRepo.isPending || !form.name || !form.url}
            className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
              text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
          >
            {createRepo.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Add Repository
          </button>
        </div>
      </div>
    </div>
  );
}
