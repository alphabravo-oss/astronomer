import { useEffect, useMemo, useState } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useDebouncedValue } from "@tanstack/react-pacer";
import {
  Search,
  Server,
  AlertTriangle,
  ChevronDown,
  ChevronRight,
  Loader2,
  X,
} from "lucide-react";
import { searchResources } from "@/lib/api/resource-search";
import type {
  SearchableResourceType,
  SearchResultRow,
} from "@/lib/api/resource-search";
import { detailHref } from "@/lib/k8s-paths";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { DataTable, type Column } from "@/components/ui/data-table";
import { DataTableQueryError } from "@/components/ui/data-table-query-error";
import { Input } from "@/components/ui/input";
import { PageHeader, PageShell } from "@/components/ui/page";
import { Select } from "@/components/ui/select";
import { StatusBadge } from "@/components/ui/status-badge";

// SEARCHABLE_TYPES is the user-facing list shown in the type dropdown.
// Keeping it in declaration order rather than alphabetical means the most
// common targets (Pods, Deployments, ...) appear first.
export const SEARCHABLE_TYPES: {
  value: SearchableResourceType;
  label: string;
}[] = [
  { value: "pods", label: "Pods" },
  { value: "deployments", label: "Deployments" },
  { value: "statefulsets", label: "StatefulSets" },
  { value: "daemonsets", label: "DaemonSets" },
  { value: "services", label: "Services" },
  { value: "ingresses", label: "Ingresses" },
  { value: "configmaps", label: "ConfigMaps" },
  { value: "secrets", label: "Secrets" },
  { value: "jobs", label: "Jobs" },
  { value: "cronjobs", label: "CronJobs" },
  { value: "persistentvolumeclaims", label: "PVCs" },
  { value: "namespaces", label: "Namespaces" },
  { value: "nodes", label: "Nodes" },
];

// searchResultHref resolves every Kubernetes object to the canonical explorer
// detail. Workload-specific pod/log/metrics tabs live there too, so operators
// no longer see two competing detail screens for the same object.
export function searchResultHref(
  resourceType: SearchableResourceType,
  cid: string,
  ns: string,
  name: string,
): string {
  switch (resourceType) {
    case "pods":
    case "deployments":
    case "statefulsets":
    case "daemonsets":
    case "jobs":
    case "cronjobs":
      return detailHref(cid, resourceType, ns || undefined, name);
    case "nodes":
      return `/dashboard/clusters/${cid}/nodes/${name}`;
    case "namespaces":
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

export const WORKLOAD_TYPES = SEARCHABLE_TYPES.filter(({ value }) =>
  [
    "pods",
    "deployments",
    "statefulsets",
    "daemonsets",
    "jobs",
    "cronjobs",
  ].includes(value),
);

export function SearchPage({
  title = "Global Search",
  description = "Search Kubernetes resources across every connected cluster",
  resourceTypes = SEARCHABLE_TYPES,
}: {
  title?: string;
  description?: string;
  resourceTypes?: typeof SEARCHABLE_TYPES;
} = {}) {
  const navigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const searchParams = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );

  // Initialize state from URL query params so a user can deep-link a
  // search (the topbar input populates `?name=...`). Keeping URL as the
  // source of truth on first mount also makes browser back/forward
  // restore the previous search.
  const [resourceType, setResourceType] = useState<SearchableResourceType>(
    resourceTypes.find(({ value }) => value === searchParams.get("type"))
      ?.value ?? resourceTypes[0].value,
  );
  const [namespace, setNamespace] = useState(
    searchParams.get("namespace") || "",
  );
  const [labelSelector, setLabelSelector] = useState(
    searchParams.get("label") || "",
  );
  const [nameFilter, setNameFilter] = useState(searchParams.get("name") || "");
  const [errorsExpanded, setErrorsExpanded] = useState(false);

  // Debounced (250ms) copies of the free-text inputs — avoids hammering
  // the search endpoint on every keystroke (Pacer's useDebouncedValue).
  const [debouncedName] = useDebouncedValue(nameFilter, { wait: 250 });
  const [debouncedLabel] = useDebouncedValue(labelSelector, { wait: 250 });
  const [debouncedNamespace] = useDebouncedValue(namespace, { wait: 250 });

  // Sync URL with debounced inputs so the URL reflects the in-flight
  // query. Using replaceState prevents history pollution on every key.
  useEffect(() => {
    const params = new URLSearchParams();
    params.set("type", resourceType);
    if (debouncedNamespace) params.set("namespace", debouncedNamespace);
    if (debouncedLabel) params.set("label", debouncedLabel);
    if (debouncedName) params.set("name", debouncedName);
    const qs = params.toString();
    window.history.replaceState(
      window.history.state,
      "",
      `${pathname}${qs ? `?${qs}` : ""}`,
    );
  }, [
    pathname,
    resourceType,
    debouncedNamespace,
    debouncedLabel,
    debouncedName,
  ]);

  const queryKey = useMemo(
    () =>
      [
        "resources-search",
        resourceType,
        debouncedNamespace,
        debouncedLabel,
        debouncedName,
      ] as const,
    [resourceType, debouncedNamespace, debouncedLabel, debouncedName],
  );

  const query = useQuery({
    queryKey,
    queryFn: ({ signal }) =>
      searchResources(
        {
          type: resourceType,
          namespace: debouncedNamespace || undefined,
          label: debouncedLabel || undefined,
          name: debouncedName || undefined,
          limit: 500,
        },
        signal,
      ),
    // 10s stale window — fan-out searches are relatively expensive and
    // the SSE invalidation below picks up real changes faster.
    staleTime: 10_000,
    refetchOnWindowFocus: false,
  });

  // Live updates: invalidate the query whenever any cluster reports a
  // k8s informer change. The hook prefix-matches so passing
  // ['resources-search'] nukes every variant the user has typed.
  useLiveQueryInvalidation(
    ["cluster.k8s_changed", "cluster.connected", "cluster.disconnected"],
    [["resources-search"]],
  );

  const data = query.data;
  const results: SearchResultRow[] = data?.results || [];
  const errors = data?.errors || [];
  const clustersQueried = data?.clustersQueried ?? 0;
  const clustersFailed = data?.clustersFailed ?? 0;

  const columns: Column<SearchResultRow>[] = [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Server className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
          <span className="font-medium text-foreground truncate">
            {row.clusterName}
          </span>
        </div>
      ),
      sortAccessor: (row) => row.clusterName,
    },
    {
      key: "namespace",
      header: "Namespace",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.namespace || "—"}
        </span>
      ),
      sortAccessor: (row) => row.namespace || "",
    },
    {
      key: "name",
      header: "Name",
      accessor: (row) => (
        <span className="font-medium text-foreground">{row.name || "—"}</span>
      ),
      sortAccessor: (row) => row.name || "",
    },
    {
      key: "type",
      header: "Type",
      accessor: (row) => (
        <span className="px-2 py-0.5 rounded-sm text-xs font-medium bg-muted text-muted-foreground">
          {row.type || resourceType}
        </span>
      ),
    },
    {
      key: "age",
      header: "Age",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground tabular-nums">
          {row.age || "—"}
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) =>
        row.status ? (
          <StatusBadge status={String(row.status)} />
        ) : (
          <span className="text-xs text-muted-foreground">—</span>
        ),
    },
  ];

  const handleRowClick = (row: SearchResultRow) => {
    // Click → per-cluster detail page. See `searchResultHref` for the
    // per-type routing table.
    const cid = row.clusterId;
    if (!cid) return;
    const ns = (row.namespace as string) || "";
    const name = (row.name as string) || "";
    void navigate({ to: searchResultHref(resourceType, cid, ns, name) });
  };

  const isEmpty = !query.isLoading && results.length === 0 && !query.isError;

  return (
    <PageShell className="space-y-4">
      {/* Sticky search bar */}
      <div className="sticky top-0 z-10 -mx-6 px-6 py-4 bg-background/95 backdrop-blur-xs border-b border-border">
        <PageHeader title={title} description={description} />

        <div className="mt-4 grid grid-cols-1 md:grid-cols-12 gap-2">
          {/* Type selector */}
          <Select
            aria-label="Resource type"
            value={resourceType}
            onChange={(e) =>
              setResourceType(e.target.value as SearchableResourceType)
            }
            containerClassName="md:col-span-2"
          >
            {resourceTypes.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </Select>

          {/* Namespace */}
          <Input
            type="text"
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
            placeholder="namespace (optional)"
            className="md:col-span-2"
          />

          {/* Label selector */}
          <Input
            type="text"
            value={labelSelector}
            onChange={(e) => setLabelSelector(e.target.value)}
            placeholder="label selector e.g. app=coredns"
            className="md:col-span-4"
          />

          {/* Name filter */}
          <div className="md:col-span-4 relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
            <Input
              type="text"
              value={nameFilter}
              onChange={(e) => setNameFilter(e.target.value)}
              placeholder="filter by name (substring)"
              data-initial-focus
              className="pl-8 pr-8"
            />
            {nameFilter && (
              <button
                onClick={() => setNameFilter("")}
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
          {query.isFetching && (
            <span className="inline-flex items-center gap-1.5">
              <Loader2 className="h-3 w-3 animate-spin" />
              Searching...
            </span>
          )}
          {!query.isFetching && data && (
            <span>
              {results.length} {results.length === 1 ? "result" : "results"}{" "}
              from {clustersQueried}{" "}
              {clustersQueried === 1 ? "cluster" : "clusters"}
            </span>
          )}
          {clustersFailed > 0 && (
            <button
              onClick={() => setErrorsExpanded((v) => !v)}
              className="inline-flex items-center gap-1.5 text-status-warning hover:underline"
            >
              <AlertTriangle className="h-3 w-3" />
              {clustersFailed} {clustersFailed === 1 ? "cluster" : "clusters"}{" "}
              failed
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
                <AlertTriangle className="h-3.5 w-3.5 text-status-warning shrink-0 mt-0.5" />
                <div className="min-w-0 flex-1">
                  <p className="font-medium text-foreground">
                    {err.cluster_name}
                  </p>
                  <p className="text-muted-foreground truncate font-mono">
                    {err.error}
                  </p>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Results */}
      <div>
        {query.isError ? (
          <DataTableQueryError
            error={query.error}
            onRetry={() => void query.refetch()}
            permission="resources:search"
          />
        ) : isEmpty ? (
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <Search className="h-10 w-10 text-muted-foreground/40 mb-3" />
            <p className="text-sm font-medium text-foreground">
              No matching resources
            </p>
            <p className="text-xs text-muted-foreground mt-1 max-w-md">
              {clustersQueried === 0
                ? "No active clusters were found. Connect a cluster and try again."
                : "Try widening the label selector or removing the namespace filter."}
            </p>
          </div>
        ) : (
          <DataTable
            data={results}
            columns={columns}
            keyExtractor={(row) =>
              `${row.clusterId}/${row.namespace || ""}/${row.name || ""}`
            }
            onRowClick={handleRowClick}
            searchPlaceholder="Filter results..."
            loading={query.isLoading}
            emptyState={{
              title: "No resources available",
              description:
                "Resources will appear here when they are available in this scope.",
            }}
          />
        )}
      </div>
    </PageShell>
  );
}
