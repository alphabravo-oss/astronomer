import { pageTableCount } from "@/lib/api/pagination";
import { Select } from "@/components/ui/select";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { KeyRound, Plus, RefreshCw, ShieldCheck, Trash2 } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ActionButton } from "@/components/ui/action-button";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  inputClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  listDeliverySources,
  type DeliverySource,
} from "@/lib/api/delivery-sources";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { AgeCell } from "@/components/ui/age-cell";
import { StackedCell } from "@/components/ui/stacked-cell";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { SourceCreateDialog } from "./-source-create-dialog";
import {
  CredentialDialog,
  SourceDeleteDialog,
  SourceVerifyDialog,
} from "./-source-dialogs";

const sourceStatuses = ["pending", "ready", "degraded", "revoked"] as const;

const sourceDataColumns: Column<DeliverySource>[] = [
  {
    key: "name",
    header: "Source",
    kind: "name",
    minSize: 180,
    accessor: (row) => <StackedCell primary={row.name} secondary={row.url} />,
    sortAccessor: (row) => row.name,
  },
  {
    key: "type",
    header: "Kind",
    kind: "badge",
    size: 104,
    minSize: 88,
    accessor: (row) => row.type.replaceAll("_", " "),
  },
  {
    key: "auth",
    header: "Authentication",
    kind: "text",
    size: 154,
    minSize: 154,
    accessor: (row) => (
      <span>
        {row.authMode.replaceAll("_", " ")}
        {row.credential.configured ? " · configured" : ""}
      </span>
    ),
  },
  {
    key: "trust",
    header: "Trust",
    kind: "badge",
    size: 140,
    minSize: 120,
    accessor: (row) =>
      row.trustPolicy.allowUnsigned ? (
        <span className="text-status-warning">Unsigned allowed</span>
      ) : (
        <span className="inline-flex items-center gap-1">
          <ShieldCheck className="h-4 w-4 text-status-success" />{" "}
          {row.trustPolicy.provider}
        </span>
      ),
  },
  {
    key: "status",
    header: "Status",
    kind: "status",
    size: 104,
    accessor: (row) => <DeliveryPhaseBadge value={row.status} />,
  },
  {
    key: "updated",
    header: "Checked",
    kind: "age",
    size: 112,
    accessor: (row) => <AgeCell value={row.lastResolvedAt} empty="Never" />,
    sortAccessor: (row) => row.lastResolvedAt ?? "",
  },
];

export function SourcesPage() {
  const { projectId, projects, projectQuery, listHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const canList = can(user, "delivery_sources", "list", scope);
  const canCreate = can(user, "delivery_sources", "create", scope);
  const canUpdate = can(user, "delivery_sources", "update", scope);
  const canDelete = can(user, "delivery_sources", "delete", scope);
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const requestedStatus = search.get("status") ?? "";
  const status = sourceStatuses.includes(
    requestedStatus as (typeof sourceStatuses)[number],
  )
    ? requestedStatus
    : undefined;
  const [pageIndex, setPageIndex] = useDeliveryPageIndex();
  const [createOpen, setCreateOpen] = useState(false);
  const [rowAction, setRowAction] = useState<{
    kind: "verify" | "rotate" | "delete";
    source: DeliverySource;
  } | null>(null);
  const verifySource = rowAction?.kind === "verify" ? rowAction.source : null;
  const rotateSource = rowAction?.kind === "rotate" ? rowAction.source : null;
  const deleteSource = rowAction?.kind === "delete" ? rowAction.source : null;
  const closeRowAction = () => setRowAction(null);
  const pageSize = 25;
  const params = {
    limit: pageSize,
    offset: pageIndex * pageSize,
    ...(status ? { status } : {}),
  };
  const setStatus = (nextStatus: string) => {
    const next = new URLSearchParams(search);
    if (nextStatus) next.set("status", nextStatus);
    else next.delete("status");
    next.delete("page");
    void navigate({
      to: `${listHref("sources")}${next.size ? `?${next.toString()}` : ""}`,
      replace: true,
    });
  };
  const query = useQuery({
    queryKey: queryKeys.delivery.sources(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliverySources(projectId, params, signal);
    },
    enabled: Boolean(projectId && canList),
    refetchInterval: liveFallback(30_000),
  });
  useLiveQueryInvalidation(
    "delivery_source.changed",
    projectId
      ? queryKeys.delivery.sourcesAll(projectId)
      : queryKeys.delivery.all,
  );

  const columns: Column<DeliverySource>[] = [
    ...sourceDataColumns,
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 192,
      minSize: 192,
      maxSize: 192,
      sortable: false,
      accessor: (row) => (
        <div className="flex justify-end gap-1">
          {canUpdate && (
            <ActionButton
              onClick={() => setRowAction({ kind: "verify", source: row })}
              aria-label={`Verify ${row.name}`}
              icon={<RefreshCw className="h-4 w-4" />}
            >
              Verify
            </ActionButton>
          )}
          {canUpdate &&
            row.authMode !== "none" &&
            row.authMode !== "workload_identity" && (
              <ActionButton
                onClick={() => setRowAction({ kind: "rotate", source: row })}
                aria-label={`Rotate credentials for ${row.name}`}
                size="icon"
                icon={<KeyRound className="h-4 w-4" />}
              />
            )}
          {canDelete && (
            <ActionButton
              onClick={() => setRowAction({ kind: "delete", source: row })}
              aria-label={`Delete ${row.name}`}
              size="icon"
              icon={<Trash2 className="h-4 w-4 text-status-error" />}
            />
          )}
        </div>
      ),
    },
  ];

  return (
    <>
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_sources:list"
        allowed={canList}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <PageHeader
            title="Sources"
            description="Reusable authenticated and verified Git, OCI, and Helm locations. Credentials are write-only."
            actions={
              canCreate ? (
                <ActionButton
                  onClick={() => setCreateOpen(true)}
                  intent="primary"
                  icon={<Plus className="h-4 w-4" />}
                >
                  Add source
                </ActionButton>
              ) : undefined
            }
          />
          <DataTable
            data={query.data?.data ?? []}
            columns={columns}
            keyExtractor={(row) => row.id}
            loading={query.isLoading}
            isError={query.isError}
            error={query.error}
            permission="delivery_sources:list"
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyState={{
              title: "No delivery sources in this project",
              description:
                "Connect a Git or OCI source for your application's manifests.",
              action: canCreate
                ? { label: "Add source", onClick: () => setCreateOpen(true) }
                : undefined,
            }}
            toolbar={
              <Select
                aria-label="Source status"
                value={status ?? ""}
                onChange={(event) => setStatus(event.target.value)}
                className={inputClass}
              >
                <option value="">All statuses</option>
                {sourceStatuses.map((value) => (
                  <option key={value} value={value}>
                    {value.replaceAll("_", " ")}
                  </option>
                ))}
              </Select>
            }
            serverSide={{
              ...pageTableCount(query.data),
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
            }}
          />
        </PageShell>
      </DeliveryProjectGate>
      {createOpen && (
        <SourceCreateDialog
          projectId={projectId}
          onClose={() => setCreateOpen(false)}
        />
      )}
      {verifySource && (
        <SourceVerifyDialog
          projectId={projectId}
          source={verifySource}
          onClose={() => closeRowAction()}
        />
      )}
      {rotateSource && (
        <CredentialDialog
          projectId={projectId}
          source={rotateSource}
          onClose={() => closeRowAction()}
        />
      )}
      <SourceDeleteDialog
        projectId={projectId}
        source={deleteSource}
        onClose={() => closeRowAction()}
      />
    </>
  );
}
