import { useMemo } from "react";
import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CheckCircle2, Clock3, Loader2, RefreshCw, RotateCcw, XCircle } from "lucide-react";

import { ActionButton } from "@/components/ui/action-button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { OverlayShell } from "@/components/ui/overlay-shell";
import { useParams, useRouter, useSearchParams } from "@/lib/navigation";
import { listClusterApps } from "@/lib/api/cluster-detail";
import {
  getCatalogOperation,
  listCatalogOperations,
  retryCatalogOperation,
  type CatalogOperation,
} from "@/lib/api/catalog";
import { queryKeys } from "@/lib/query-keys";
import { cn } from "@/lib/utils";
import { toastApiError, toastSuccess } from "@/lib/toast";

function OperationsPage() {
  const { id: clusterId } = useParams() as { id: string };
  const search = useSearchParams();
  const router = useRouter();
  const queryClient = useQueryClient();
  const selectedId = search?.get("operation") ?? "";
  const apps = useQuery({
    queryKey: queryKeys.clusterPages.appsInstalled(clusterId),
    queryFn: () => listClusterApps(clusterId, { limit: 500 }),
  });
  const operations = useQuery({
    queryKey: queryKeys.catalog.operations,
    queryFn: listCatalogOperations,
    refetchInterval: 5_000,
  });
  const appIds = useMemo(
    () => new Set((apps.data?.items ?? []).map((app) => app.id)),
    [apps.data?.items],
  );
  const appNames = useMemo(
    () => new Map((apps.data?.items ?? []).map((app) => [app.id, app.releaseName])),
    [apps.data?.items],
  );
  const rows = useMemo(
    () => (operations.data ?? []).filter((operation) => appIds.has(operation.targetKey ?? "")),
    [appIds, operations.data],
  );
  const selected = useQuery({
    queryKey: queryKeys.catalog.operation(selectedId),
    queryFn: () => getCatalogOperation(selectedId),
    enabled: Boolean(selectedId),
    refetchInterval: (query) => {
      const status = (query.state.data as CatalogOperation | undefined)?.status;
      return status === "completed" || status === "failed" || status === "superseded" ? false : 2_000;
    },
  });
  const retry = useMutation({
    mutationFn: retryCatalogOperation,
    onSuccess: (operation) => {
      toastSuccess("Operation requeued");
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.operations });
      void queryClient.invalidateQueries({ queryKey: queryKeys.catalog.operation(operation.id ?? "") });
    },
    onError: (error) => toastApiError("Retry failed", error),
  });
  const columns: Column<CatalogOperation>[] = [
    { key: "operationType", header: "Action", accessor: (row) => <span className="font-medium capitalize text-foreground">{row.operationType}</span>, sortable: true, filter: { label: "Actions" } },
    { key: "targetKey", header: "Release", accessor: (row) => appNames.get(row.targetKey ?? "") || row.targetKey || "—", sortAccessor: (row) => appNames.get(row.targetKey ?? "") || "", sortable: true },
    { key: "status", header: "Status", accessor: (row) => <OperationStatus status={row.status ?? "unknown"} />, sortAccessor: (row) => row.status ?? "", sortable: true, filter: { label: "Statuses" } },
    { key: "attemptCount", header: "Attempts", accessor: (row) => row.attemptCount ?? 0, sortable: true, numeric: true },
    { key: "createdAt", header: "Started", accessor: (row) => row.createdAt ? new Date(row.createdAt).toLocaleString() : "—", sortAccessor: (row) => row.createdAt ?? "", sortable: true },
    { key: "updatedAt", header: "Updated", accessor: (row) => row.updatedAt ? new Date(row.updatedAt).toLocaleString() : "—", sortAccessor: (row) => row.updatedAt ?? "", sortable: true },
  ];

  return (
    <div className="space-y-6 p-4">
      <header className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between"><div><h1 className="flex items-center gap-2 text-2xl font-semibold text-foreground"><Activity className="h-6 w-6" /> App operations</h1><p className="mt-1 text-sm text-muted-foreground">Flux-backed install, upgrade, rollback, and uninstall activity for this cluster.</p></div><ActionButton icon={<RefreshCw className="h-4 w-4" />} loading={operations.isFetching} onClick={() => operations.refetch()}>Refresh</ActionButton></header>
      <DataTable data={rows} columns={columns} keyExtractor={(row) => row.id ?? `${row.targetKey}-${row.createdAt}`} loading={operations.isLoading || apps.isLoading} isError={operations.isError || apps.isError} onRetry={() => { void operations.refetch(); void apps.refetch(); }} emptyMessage="No application operations have run on this cluster yet." searchable searchPlaceholder="Search operations…" persistKey="cluster-app-operations" onRowClick={(row) => router.replace(`/dashboard/clusters/${clusterId}/apps/operations?operation=${encodeURIComponent(row.id ?? "")}`)} />
      {selectedId && <OperationDetails operation={selected.data} loading={selected.isLoading} onClose={() => router.replace(`/dashboard/clusters/${clusterId}/apps/operations`)} onRetry={(id) => retry.mutate(id)} retrying={retry.isPending} />}
    </div>
  );
}

function OperationDetails({ operation, loading, onClose, onRetry, retrying }: { operation?: CatalogOperation; loading: boolean; onClose: () => void; onRetry: (id: string) => void; retrying: boolean }) {
  return <OverlayShell onClose={onClose} rootClassName="p-4"><div className="relative max-h-[88vh] w-full max-w-2xl overflow-y-auto rounded-2xl border border-border bg-card p-6 shadow-2xl" role="dialog" aria-modal="true">{loading || !operation ? <div className="flex items-center gap-2 text-sm text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" />Loading operation…</div> : <><div className="flex items-start justify-between gap-4"><div><h2 className="text-xl font-semibold capitalize text-foreground">{operation.operationType} operation</h2><div className="mt-2"><OperationStatus status={operation.status ?? "unknown"} /></div></div><ActionButton onClick={onClose}>Close</ActionButton></div><div className="mt-5 grid gap-3 rounded-lg border border-border bg-muted/15 p-4 text-sm sm:grid-cols-2"><div><span className="text-muted-foreground">Target</span><div className="break-all font-mono text-xs text-foreground">{operation.targetKey}</div></div><div><span className="text-muted-foreground">Attempts</span><div className="text-foreground">{operation.attemptCount ?? 0}</div></div></div><div className="mt-5 space-y-2">{(operation.events ?? []).map((event, index) => <div key={event.id ?? index} className="flex gap-3 rounded-lg border border-border p-3"><CheckCircle2 className={cn("mt-0.5 h-4 w-4 flex-none", event.level === "error" ? "text-status-error" : "text-status-success")} /><div><div className="flex items-center gap-2"><span className="text-xs font-semibold uppercase tracking-wide text-foreground">{event.stage}</span><span className="text-[10px] text-muted-foreground">{event.createdAt ? new Date(event.createdAt).toLocaleString() : ""}</span></div><p className="mt-1 text-sm text-muted-foreground">{event.message}</p></div></div>)}</div>{operation.errorMessage && <div className="mt-4 rounded-lg border border-status-error/30 bg-status-error/10 p-3 text-sm text-status-error">{operation.errorMessage}</div>}{(operation.status === "failed" || operation.status === "superseded") && <div className="mt-5 flex justify-end"><ActionButton intent="primary" icon={<RotateCcw className="h-4 w-4" />} loading={retrying} onClick={() => onRetry(operation.id ?? "")}>Retry operation</ActionButton></div>}</>}</div></OverlayShell>;
}

function OperationStatus({ status }: { status: string }) {
  const running = status === "pending" || status === "running";
  const failed = status === "failed" || status === "superseded";
  const Icon = status === "completed" ? CheckCircle2 : failed ? XCircle : running ? Clock3 : Activity;
  return <span className={cn("inline-flex items-center gap-1.5 rounded-full border px-2 py-1 text-xs font-medium capitalize", status === "completed" ? "border-status-success/30 bg-status-success/10 text-status-success" : failed ? "border-status-error/30 bg-status-error/10 text-status-error" : "border-status-info/30 bg-status-info/10 text-status-info")}><Icon className={cn("h-3.5 w-3.5", running && "animate-pulse")} />{status}</span>;
}

export const Route = createFileRoute("/dashboard/clusters/$id/apps/operations/")({
  validateSearch: (search: Record<string, unknown>) => search as { operation?: string },
  component: OperationsPage,
});
