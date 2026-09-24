import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Trash2, XCircle } from "lucide-react";
import { useMemo, useState } from "react";

import { ActionButton } from "@/components/ui/action-button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DataTable, type DataTableProps } from "@/components/ui/data-table";
import { ModalShell } from "@/components/ui/modal-shell";
import { k8sDelete } from "@/lib/api/kubernetes-proxy";
import { useClusterNamespaceScope } from "@/lib/cluster-scope";
import { canonicalPermissionResource } from "@/lib/permission-hooks";
import { queryKeys } from "@/lib/query-keys";
import { toastSuccess } from "@/lib/toast";
import { useStorageSnapshot } from "@/lib/hooks/use-storage-snapshot";
import { Checkbox } from "@/components/ui/checkbox";
import {
  BulkWorkloadActions,
  type ExplorerBulkWorkloads,
} from "./bulk-workload-actions";
import {
  supportsBulkWorkload,
  type BulkWorkloadTarget,
  type BulkWorkloadAction,
} from "./bulk-workload-model";

export interface BulkDeleteResult {
  key: string;
  label: string;
  ok: boolean;
  error?: string;
}

export interface ExplorerBulkDelete<T> {
  /** Build the exact Kubernetes proxy path for one selected object. */
  path: (row: T) => string;
  label?: (row: T) => string;
  noun?: string;
}

type ExplorerDataTableProps<T> = Omit<
  DataTableProps<T>,
  "data" | "persistKey" | "selectable" | "bulkActions"
> & {
  clusterId: string;
  resourceType: string;
  data: T[];
  namespaceAccessor?: (row: T) => string | undefined;
  bulkDelete?: ExplorerBulkDelete<T>;
  bulkWorkloads?: ExplorerBulkWorkloads<T>;
};

async function runWithConcurrency<T, R>(
  values: readonly T[],
  concurrency: number,
  operation: (value: T) => Promise<R>,
): Promise<R[]> {
  const results = new Array<R>(values.length);
  let cursor = 0;
  await Promise.all(
    Array.from({ length: Math.min(concurrency, values.length) }, async () => {
      while (cursor < values.length) {
        const index = cursor++;
        results[index] = await operation(values[index]);
      }
    }),
  );
  return results;
}

export async function deleteExplorerResources<T>(
  clusterId: string,
  rows: readonly T[],
  config: ExplorerBulkDelete<T>,
  keyExtractor: (row: T) => string,
): Promise<BulkDeleteResult[]> {
  return runWithConcurrency(rows, 5, async (row) => {
    const key = keyExtractor(row);
    const label = config.label?.(row) ?? key;
    try {
      await k8sDelete(clusterId, config.path(row));
      return { key, label, ok: true };
    } catch (error) {
      return {
        key,
        label,
        ok: false,
        error: error instanceof Error ? error.message : "Delete failed",
      };
    }
  });
}

function BulkDeleteAction<T>({
  clusterId,
  resourceType,
  rows,
  config,
  keyExtractor,
}: {
  clusterId: string;
  resourceType: string;
  rows: T[];
  config: ExplorerBulkDelete<T>;
  keyExtractor: (row: T) => string;
}) {
  const queryClient = useQueryClient();
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [running, setRunning] = useState(false);
  const [results, setResults] = useState<BulkDeleteResult[] | null>(null);
  const labels = rows.map((row) => config.label?.(row) ?? keyExtractor(row));
  const noun = config.noun ?? resourceType.replace(/s$/, "");

  const run = async () => {
    setRunning(true);
    const next = await deleteExplorerResources(
      clusterId,
      rows,
      config,
      keyExtractor,
    );
    setRunning(false);
    setConfirmOpen(false);
    setResults(next);
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.k8s.all }),
      queryClient.invalidateQueries({ queryKey: queryKeys.clusters.all }),
      queryClient.invalidateQueries({ queryKey: queryKeys.networking.all }),
      queryClient.invalidateQueries({ queryKey: queryKeys.storage.all }),
      queryClient.invalidateQueries({ queryKey: queryKeys.workloads.all }),
      queryClient.invalidateQueries({ queryKey: queryKeys.generic.all }),
    ]);
    const succeeded = next.filter((result) => result.ok).length;
    if (succeeded > 0) toastSuccess(`Deleted ${succeeded} of ${next.length}`);
  };

  return (
    <>
      <ActionButton
        size="sm"
        intent="destructive"
        icon={<Trash2 className="h-3.5 w-3.5" />}
        onClick={() => setConfirmOpen(true)}
      >
        Delete selected
      </ActionButton>
      <ConfirmDialog
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        onConfirm={() => void run()}
        title={`Delete ${rows.length} ${noun}${rows.length === 1 ? "" : "s"}`}
        description="The API authorizes and audits each object independently. Objects that fail are left untouched."
        confirmValue={`delete ${rows.length}`}
        confirmText={`Delete ${rows.length}`}
        variant="destructive"
        loading={running}
        impact={{
          scope: `${clusterId} / ${labels.join(", ")}`,
          consequences: ["Each listed Kubernetes object will be deleted."],
          recovery:
            "Re-apply the original manifest or restore from a verified backup.",
        }}
      >
        <div className="max-h-40 overflow-y-auto rounded-md border border-border bg-muted/20 p-2">
          <ul className="space-y-1 font-mono text-xs text-foreground">
            {labels.map((label) => (
              <li key={label} className="break-all">
                {label}
              </li>
            ))}
          </ul>
        </div>
      </ConfirmDialog>
      {results ? (
        <ModalShell
          title="Bulk delete results"
          subtitle={`${results.filter((result) => result.ok).length} succeeded, ${results.filter((result) => !result.ok).length} failed`}
          onClose={() => setResults(null)}
          size="md"
          footer={
            <ActionButton
              size="sm"
              intent="primary"
              onClick={() => setResults(null)}
            >
              Done
            </ActionButton>
          }
          footerClassName="flex justify-end"
        >
          <ul className="max-h-80 divide-y divide-border overflow-y-auto rounded-md border border-border">
            {results.map((result) => (
              <li
                key={result.key}
                className="flex items-start gap-2 px-3 py-2 text-sm"
              >
                {result.ok ? (
                  <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-status-success" />
                ) : (
                  <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-status-error" />
                )}
                <span className="min-w-0">
                  <span className="block break-all font-mono text-xs text-foreground">
                    {result.label}
                  </span>
                  {result.error ? (
                    <span className="mt-0.5 block text-xs text-status-error">
                      {result.error}
                    </span>
                  ) : null}
                </span>
              </li>
            ))}
          </ul>
        </ModalShell>
      ) : null}
    </>
  );
}

/**
 * Explorer-owned table policy: stable per-resource preferences, global
 * namespace scope, and optional per-object authorized/audited bulk deletion.
 */
export function ExplorerDataTable<T extends object>({
  clusterId,
  resourceType,
  data,
  namespaceAccessor,
  bulkDelete,
  bulkWorkloads,
  keyExtractor,
  ...tableProps
}: ExplorerDataTableProps<T>) {
  const scope = useClusterNamespaceScope(clusterId);
  const [grouping, persistGrouping] = useStorageSnapshot(
    `explorer:${resourceType}:namespace-grouping`,
  );
  const namespaceLabel = (row: T) =>
    namespaceAccessor?.(row) ?? (row as { namespace?: string }).namespace;
  const hasNamespaces = data.some((row) => !!namespaceLabel(row));
  const permissionResource = canonicalPermissionResource(resourceType);
  const visibleData = useMemo(
    () =>
      data.filter((row) =>
        scope.includes(
          namespaceAccessor?.(row) ?? (row as { namespace?: string }).namespace,
        ),
      ),
    [data, namespaceAccessor, scope],
  );
  const canDelete = (row: T) => {
    if (!bulkDelete) return false;
    try {
      bulkDelete.path(row);
      return scope.allows(
        permissionResource,
        "delete",
        namespaceAccessor?.(row) ?? (row as { namespace?: string }).namespace,
      );
    } catch {
      return false;
    }
  };
  const canOperate = (target: BulkWorkloadTarget, action: BulkWorkloadAction) =>
    supportsBulkWorkload(target, action) &&
    scope.allows(permissionResource, action, target.namespace);
  const canSelect = (row: T) =>
    canDelete(row) ||
    (!!bulkWorkloads &&
      (canOperate(bulkWorkloads.target(row), "scale") ||
        canOperate(bulkWorkloads.target(row), "restart")));

  return (
    <DataTable
      {...tableProps}
      groupBy={
        grouping === "true" && hasNamespaces
          ? (row) => `Namespace: ${namespaceLabel(row) || "Cluster-scoped"}`
          : undefined
      }
      toolbar={
        <>
          {tableProps.toolbar}
          {hasNamespaces && (
            <label className="flex items-center gap-2 text-xs">
              <Checkbox
                checked={grouping === "true"}
                onChange={(event) => persistGrouping(event.target.checked)}
              />
              Group namespaces{tableProps.serverSide ? " (this page)" : ""}
            </label>
          )}
        </>
      }
      data={visibleData}
      keyExtractor={keyExtractor}
      persistKey={`explorer:${resourceType}`}
      selectable={bulkDelete || bulkWorkloads ? canSelect : false}
      bulkActions={
        bulkDelete || bulkWorkloads
          ? (selected) => (
              <>
                {bulkWorkloads && (
                  <BulkWorkloadActions
                    clusterId={clusterId}
                    rows={selected}
                    config={bulkWorkloads}
                    allowed={canOperate}
                  />
                )}
                {bulkDelete && selected.every(canDelete) && (
                  <BulkDeleteAction
                    clusterId={clusterId}
                    resourceType={resourceType}
                    rows={selected}
                    config={bulkDelete}
                    keyExtractor={keyExtractor}
                  />
                )}
              </>
            )
          : undefined
      }
    />
  );
}
