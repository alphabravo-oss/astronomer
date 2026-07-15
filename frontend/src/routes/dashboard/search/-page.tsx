import { useEffect, useMemo, useState } from 'react';
import { useRouter, useSearchParams } from '@/lib/navigation';
import { useQuery } from '@tanstack/react-query';
import {
  Search,
  Server,
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Loader2,
  X,
} from 'lucide-react';
import {
  searchResources,
  type SearchableResourceType,
  type SearchResultRow,
} from '@/lib/api';
import { detailHref } from '@/lib/k8s-paths';
import { useLiveQueryInvalidation } from '@/lib/live/hooks';
import { DataTable, type Column } from '@/components/ui/data-table';
import { StatusBadge } from '@/components/ui/status-badge';
import { cn } from '@/lib/utils';

// SEARCHABLE_TYPES is the user-facing list shown in the type dropdown.
// Keeping it in declaration order rather than alphabetical means the most
// common targets (Pods, Deployments, ...) appear first.
export const SEARCHABLE_TYPES: { value: SearchableResourceType; label: string }[] = [
  { value: 'pods', label: 'Pods' },
  { value: 'deployments', label: 'Deployments' },
  { value: 'statefulsets', label: 'StatefulSets' },
  { value: 'daemonsets', label: 'DaemonSets' },
  { value: 'services', label: 'Services' },
  { value: 'ingresses', label: 'Ingresses' },
  { value: 'configmaps', label: 'ConfigMaps' },
  { value: 'secrets', label: 'Secrets' },
  { value: 'jobs', label: 'Jobs' },
  { value: 'cronjobs', label: 'CronJobs' },
  { value: 'persistentvolumeclaims', label: 'PVCs' },
  { value: 'namespaces', label: 'Namespaces' },
  { value: 'nodes', label: 'Nodes' },
];

// searchResultHref resolves the in-app destination for a clicked search
// result. Pods and other workload kinds land on their dedicated workload
// detail; nodes/namespaces on their list/detail pages; everything else
// (Services, Ingresses, ConfigMaps, Secrets, PVCs, ...) deep-links the
// generic per-cluster detail route via `detailHref`, falling back to the
// resource list when the row carries no name.
export function searchResultHref(
  resourceType: SearchableResourceType,
  cid: string,
  ns: string,
  name: string,
): string {
  switch (resourceType) {
    case 'pods':
      return `/dashboard/clusters/${cid}/workloads/pods/${ns}/${name}`;
    case 'deployments':
    case 'statefulsets':
    case 'daemonsets':
    case 'jobs':
    case 'cronjobs': {
      const kind = resourceType.replace(/s$/, '');
      return `/dashboard/clusters/${cid}/workloads/${kind.toLowerCase()}/${ns}/${name}`;
    }
    case 'nodes':
      return `/dashboard/clusters/${cid}/nodes/${name}`;
    case 'namespaces':
      return `/dashboard/clusters/${cid}/namespaces`;
    default:
      // Generic k8s objects (services, ingresses, configmaps, secrets, pvcs).
      // With a name we deep-link the detail route (matches resolveDetailSlug);
      // without one we fall back to the cluster's resource list.
      return name
        ? detailHref(cid, resourceType, ns || undefined, name)
        : `/dashboard/clusters/${cid}/${resourceType}`;
  }
}

// useDebouncedValue returns a value that only updates after `delay`ms of
// stillness. Avoids hammering the search endpoint on every keystroke.
function useDebouncedValue<T>(value: T, delay = 250): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), delay);
    return () => clearTimeout(id);
  }, [value, delay]);
  return debounced;
}

export function SearchPage() {
  const router = useRouter();
  const searchParams = useSearchParams();

  // Initialize state from URL query params so a user can deep-link a
  // search (the topbar input populates `?name=...`). Keeping URL as the
  // source of truth on first mount also makes browser back/forward
  // restore the previous search.
  const [resourceType, setResourceType] = useState<SearchableResourceType>(
    (searchParams.get('type') as SearchableResourceType) || 'pods'
  );
  const [namespace, setNamespace] = useState(searchParams.get('namespace') || '');
  const [labelSelector, setLabelSelector] = useState(searchParams.get('label') || '');
  const [nameFilter, setNameFilter] = useState(searchParams.get('name') || '');
  const [errorsExpanded, setErrorsExpanded] = useState(false);

  const debouncedName = useDebouncedValue(nameFilter, 250);
  const debouncedLabel = useDebouncedValue(labelSelector, 250);
  const debouncedNamespace = useDebouncedValue(namespace, 250);

  // Sync URL with debounced inputs so the URL reflects the in-flight
  // query. Using replaceState prevents history pollution on every key.
  useEffect(() => {
    const params = new URLSearchParams();
    params.set('type', resourceType);
    if (debouncedNamespace) params.set('namespace', debouncedNamespace);
    if (debouncedLabel) params.set('label', debouncedLabel);
    if (debouncedName) params.set('name', debouncedName);
    const qs = params.toString();
    window.history.replaceState({}, '', `/dashboard/search${qs ? `?${qs}` : ''}`);
  }, [resourceType, debouncedNamespace, debouncedLabel, debouncedName]);

  const queryKey = useMemo(
    () => [
      'resources-search',
      resourceType,
      debouncedNamespace,
      debouncedLabel,
      debouncedName,
    ] as const,
    [resourceType, debouncedNamespace, debouncedLabel, debouncedName]
  );

  const {
    data,
    isLoading,
    isFetching,
    error,
  } = useQuery({
    queryKey,
    queryFn: () =>
      searchResources({
        type: resourceType,
        namespace: debouncedNamespace || undefined,
        label: debouncedLabel || undefined,
        name: debouncedName || undefined,
        limit: 500,
      }),
    // 10s stale window — fan-out searches are relatively expensive and
    // the SSE invalidation below picks up real changes faster.
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  });

  // Live updates: invalidate the query whenever any cluster reports a
  // k8s informer change. The hook prefix-matches so passing
  // ['resources-search'] nukes every variant the user has typed.
  useLiveQueryInvalidation(
    ['cluster.k8s_changed', 'cluster.connected', 'cluster.disconnected'],
    [['resources-search']]
  );

  const results: SearchResultRow[] = data?.results || [];
  const errors = data?.errors || [];
  const clustersQueried = data?.clustersQueried ?? 0;
  const clustersFailed = data?.clustersFailed ?? 0;

  const columns: Column<SearchResultRow>[] = [
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Server className="h-3.5 w-3.5 text-muted-foreground flex-shrink-0" />
          <span className="font-medium text-foreground truncate">{row.clusterName}</span>
        </div>
      ),
      sortAccessor: (row) => row.clusterName,
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.namespace || '—'}
        </span>
      ),
      sortAccessor: (row) => row.namespace || '',
    },
    {
      key: 'name',
      header: 'Name',
      accessor: (row) => (
        <span className="font-medium text-foreground">{row.name || '—'}</span>
      ),
      sortAccessor: (row) => row.name || '',
    },
    {
      key: 'type',
      header: 'Type',
      accessor: (row) => (
        <span className="px-2 py-0.5 rounded text-xs font-medium bg-muted text-muted-foreground">
          {row.type || resourceType}
        </span>
      ),
    },
    {
      key: 'age',
      header: 'Age',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">{row.age || '—'}</span>
      ),
    },
    {
      key: 'status',
      header: 'Status',
      accessor: (row) =>
        row.status ? <StatusBadge status={String(row.status)} /> : <span className="text-xs text-muted-foreground">—</span>,
    },
  ];

  const handleRowClick = (row: SearchResultRow) => {
    // Click → per-cluster detail page. See `searchResultHref` for the
    // per-type routing table.
    const cid = row.clusterId;
    if (!cid) return;
    const ns = (row.namespace as string) || '';
    const name = (row.name as string) || '';
    router.push(searchResultHref(resourceType, cid, ns, name));
  };

  const isEmpty = !isLoading && results.length === 0 && !error;

  return (
    <div className="space-y-4">
      {/* Sticky search bar */}
      <div className="sticky top-0 z-10 -mx-6 px-6 py-4 bg-background/95 backdrop-blur-sm border-b border-border">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Global Search</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Search Kubernetes resources across every connected cluster
          </p>
        </div>

        <div className="mt-4 grid grid-cols-1 md:grid-cols-12 gap-2">
          {/* Type selector */}
          <select
            value={resourceType}
            onChange={(e) => setResourceType(e.target.value as SearchableResourceType)}
            className="md:col-span-2 h-9 px-3 rounded-md border border-border bg-background text-sm
              text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
          >
            {SEARCHABLE_TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </select>

          {/* Namespace */}
          <input
            type="text"
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
            placeholder="namespace (optional)"
            className="md:col-span-2 h-9 px-3 rounded-md border border-border bg-background text-sm
              text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
          />

          {/* Label selector */}
          <input
            type="text"
            value={labelSelector}
            onChange={(e) => setLabelSelector(e.target.value)}
            placeholder="label selector e.g. app=coredns"
            className="md:col-span-4 h-9 px-3 rounded-md border border-border bg-background text-sm
              text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring font-mono"
          />

          {/* Name filter */}
          <div className="md:col-span-4 relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
            <input
              type="text"
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              placeholder="filter by name (substring)"
              autoFocus
              className="w-full h-9 pl-8 pr-8 rounded-md border border-border bg-background text-sm
                text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            />
            {nameFilter && (
              <button
                onClick={() => setNameFilter('')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                aria-label="Clear search"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        </div>

        {/* Status line */}
        <div className="mt-3 flex items-center gap-3 text-xs text-muted-foreground">
          {isFetching && (
            <span className="inline-flex items-center gap-1.5">
              <Loader2 className="h-3 w-3 animate-spin" />
              Searching...
            </span>
          )}
          {!isFetching && data && (
            <span>
              {results.length} {results.length === 1 ? 'result' : 'results'} from {clustersQueried}{' '}
              {clustersQueried === 1 ? 'cluster' : 'clusters'}
            </span>
          )}
          {clustersFailed > 0 && (
            <button
              onClick={() => setErrorsExpanded((v) => !v)}
              className="inline-flex items-center gap-1.5 text-status-warning hover:underline"
            >
              <AlertTriangle className="h-3 w-3" />
              {clustersFailed} {clustersFailed === 1 ? 'cluster' : 'clusters'} failed
              {errorsExpanded ? (
                <ChevronDown className="h-3 w-3" />
              ) : (
                <ChevronRight className="h-3 w-3" />
              )}
            </button>
          )}
        </div>

        {/* Per-cluster errors (collapsible) */}
        {errorsExpanded && errors.length > 0 && (
          <div className="mt-2 rounded-md border border-status-warning/30 bg-status-warning/5 p-3 space-y-1.5">
            {errors.map((err) => (
              <div
                key={err.cluster_id}
                className="flex items-start gap-2 text-xs"
              >
                <AlertTriangle className="h-3.5 w-3.5 text-status-warning flex-shrink-0 mt-0.5" />
                <div className="min-w-0 flex-1">
                  <p className="font-medium text-foreground">{err.cluster_name}</p>
                  <p className="text-muted-foreground truncate font-mono">{err.error}</p>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Results */}
      <div>
        {error ? (
          <div className="flex items-center gap-3 p-4 rounded-lg border border-status-error/30 bg-status-error/5 text-sm">
            <AlertTriangle className="h-4 w-4 text-status-error" />
            <span className="text-foreground">
              Search failed: {error instanceof Error ? error.message : 'unknown error'}
            </span>
          </div>
        ) : isEmpty ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <Search className="h-10 w-10 text-muted-foreground/40 mb-3" />
            <p className="text-sm font-medium text-foreground">No matching resources</p>
            <p className="text-xs text-muted-foreground mt-1 max-w-md">
              {clustersQueried === 0
                ? 'No active clusters were found. Connect a cluster and try again.'
                : 'Try widening the label selector or removing the namespace filter.'}
            </p>
          </div>
        ) : (
          <DataTable
            data={results}
            columns={columns}
            keyExtractor={(row) =>
              `${row.clusterId}/${row.namespace || ''}/${row.name || ''}`
            }
            onRowClick={handleRowClick}
            searchPlaceholder="Filter results..."
            loading={isLoading}
            emptyMessage="No resources match your query"
          />
        )}
      </div>

      <p className={cn('text-2xs text-muted-foreground text-center pt-2', !data && 'invisible')}>
        Tip: hit Cmd+K from anywhere to focus the global search input.
      </p>
    </div>
  );
}
