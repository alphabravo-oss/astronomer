import { pageTableCount } from "@/lib/api/pagination";
import { Select } from "@/components/ui/select";
import { useQuery } from "@tanstack/react-query";
import { Rocket } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  inputClass,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  listDeliveryRollouts,
  rolloutIsTerminal,
  type DeliveryRollout,
  type RolloutState,
} from "@/lib/api/delivery-rollouts";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { AgeCell } from "@/components/ui/age-cell";
import { StackedCell } from "@/components/ui/stacked-cell";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";

const states: RolloutState[] = [
  "draft",
  "resolving",
  "awaiting_approval",
  "queued",
  "progressing",
  "paused",
  "succeeded",
  "failed",
  "rolling_back",
  "rolled_back",
  "rollback_failed",
  "rejected",
  "aborted",
];

export function RolloutsPage() {
  const { projectId, projects, projectQuery, listHref, entityHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const allowed = can(user, "delivery_rollouts", "list", {
    type: "project",
    id: projectId,
  });
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const stateValue = search.get("state") ?? "";
  const state = states.includes(stateValue as RolloutState)
    ? (stateValue as RolloutState)
    : undefined;
  const pageIndex = Math.max(0, Number(search.get("page") ?? 0) || 0);
  const pageSize = 25;
  const params = {
    limit: pageSize,
    offset: pageIndex * pageSize,
    ...(state ? { state } : {}),
  };
  const setFilters = (nextState: string, nextPage = 0) => {
    const next = new URLSearchParams(search);
    if (nextState) next.set("state", nextState);
    else next.delete("state");
    if (nextPage) next.set("page", String(nextPage));
    else next.delete("page");
    void navigate({
      to: `${listHref("rollouts")}?${next.toString()}`,
      replace: true,
    });
  };
  const query = useQuery({
    queryKey: queryKeys.delivery.rollouts(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliveryRollouts(projectId, params, signal);
    },
    enabled: Boolean(projectId && allowed),
    refetchInterval: (current) => {
      const rows = current.state.data?.data ?? [];
      return rows.some((row) => !rolloutIsTerminal(row.state))
        ? liveFallback(5_000)()
        : liveFallback(30_000)();
    },
  });
  useLiveQueryInvalidation(
    "delivery_rollout.changed",
    projectId
      ? queryKeys.delivery.rolloutsAll(projectId)
      : queryKeys.delivery.all,
  );
  const columns: Column<DeliveryRollout>[] = [
    {
      key: "id",
      header: "Rollout",
      kind: "name",
      minSize: 280,
      accessor: (row) => (
        <div className="flex min-w-0 items-center gap-2">
          <Rocket className="h-4 w-4 shrink-0 text-muted-foreground" />
          <StackedCell
            primary={row.id}
            primaryClassName="font-mono text-xs font-normal"
            secondary={`target generation ${row.targetGeneration}`}
          />
        </div>
      ),
      sortAccessor: (row) => row.id,
    },
    {
      key: "state",
      header: "State",
      kind: "status",
      accessor: (row) => <DeliveryPhaseBadge value={row.state} />,
    },
    {
      key: "strategy",
      header: "Strategy",
      kind: "text",
      size: 140,
      minSize: 112,
      accessor: (row) => row.strategy.type.replaceAll("_", " "),
    },
    {
      key: "progress",
      header: "Progress",
      kind: "percent",
      size: 176,
      minSize: 160,
      maxSize: 220,
      align: "left",
      sortAccessor: (row) =>
        row.totalClusters ? row.readyClusters / row.totalClusters : 0,
      accessor: (row) => (
        <div className="w-full">
          <p className="text-sm tabular-nums">
            {row.readyClusters}/{row.totalClusters} ready
          </p>
          <div className="mt-1 h-1.5 overflow-hidden rounded-sm bg-muted">
            <div
              className="h-full bg-status-success"
              style={{
                width: `${row.totalClusters ? Math.round((row.readyClusters / row.totalClusters) * 100) : 0}%`,
              }}
            />
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {row.failedClusters} failed · {row.blockedClusters} blocked
          </p>
        </div>
      ),
    },
    {
      key: "revision",
      header: "Version",
      kind: "id",
      size: 160,
      minSize: 140,
      accessor: (row) => row.toBundleVersionId,
      sortAccessor: (row) => row.toBundleVersionId,
    },
    {
      key: "updated",
      header: "Updated",
      kind: "age",
      accessor: (row) => <AgeCell value={row.updatedAt} />,
      sortAccessor: (row) => row.updatedAt,
    },
  ];
  return (
    <DeliveryProjectGate
      projectId={projectId}
      loading={projectQuery.isLoading}
      error={projectQuery.isError}
      projectsCount={projects.length}
      permission="delivery_rollouts:list"
      allowed={allowed}
      onRetry={() => void projectQuery.refetch()}
    >
      <PageShell>
        <PageHeader
          title="Rollouts"
          description="Immutable placement attempts with fenced actions, approvals, cohorts, budgets, and known-good rollback."
        />
        <DataTable
          data={query.data?.data ?? []}
          columns={columns}
          keyExtractor={(row) => row.id}
          loading={query.isLoading}
          isError={query.isError}
          error={query.error}
          permission="delivery_rollouts:list"
          onRetry={() => void query.refetch()}
          searchable={false}
          filtersActive={!!state}
          onClearFilters={() => setFilters("")}
          emptyState={{
            title: "No rollouts yet",
            description:
              "Create a rollout from a component bundle to deliver it to the selected targets.",
          }}
          toolbar={
            <Select
              aria-label="Rollout state"
              value={state ?? ""}
              onChange={(e) => setFilters(e.target.value)}
              className={inputClass}
            >
              <option value="">All states</option>
              {states.map((value) => (
                <option key={value} value={value}>
                  {value.replaceAll("_", " ")}
                </option>
              ))}
            </Select>
          }
          onRowClick={(row) =>
            void navigate({ to: entityHref("rollouts", row.id) })
          }
          serverSide={{
            ...pageTableCount(query.data),
            pagination: { pageIndex, pageSize },
            onPaginationChange: (next) =>
              setFilters(state ?? "", next.pageIndex),
          }}
        />
      </PageShell>
    </DeliveryProjectGate>
  );
}
