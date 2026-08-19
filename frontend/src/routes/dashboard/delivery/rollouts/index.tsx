import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { Rocket } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  RedirectDeliveryList,
  inputClass,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  listDeliveryRollouts,
  rolloutIsTerminal,
  type DeliveryRollout,
  type RolloutState,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useRouter, useSearchParams } from "@/lib/navigation";
import { formatRelativeTime } from "@/lib/utils";
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
  const search = useSearchParams();
  const router = useRouter();
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
    router.replace(`${listHref("rollouts")}?${next.toString()}`);
  };
  const query = useQuery({
    queryKey: queryKeys.delivery.rollouts(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliveryRollouts(projectId, params);
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
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Rocket className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-mono text-xs">{row.id}</p>
            <p className="text-xs text-muted-foreground">
              target generation {row.targetGeneration}
            </p>
          </div>
        </div>
      ),
    },
    {
      key: "state",
      header: "State",
      accessor: (row) => <DeliveryPhaseBadge value={row.state} />,
    },
    {
      key: "strategy",
      header: "Strategy",
      accessor: (row) => row.strategy.type.replaceAll("_", " "),
    },
    {
      key: "progress",
      header: "Progress",
      accessor: (row) => (
        <div className="min-w-36">
          <p className="text-sm tabular-nums">
            {row.readyClusters}/{row.totalClusters} ready
          </p>
          <div className="mt-1 h-1.5 overflow-hidden rounded bg-muted">
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
      header: "Desired version",
      accessor: (row) => (
        <code className="text-xs">{row.toBundleVersionId}</code>
      ),
    },
    {
      key: "updated",
      header: "Updated",
      accessor: (row) => formatRelativeTime(row.updatedAt),
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
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyMessage="No rollouts match this filter"
            toolbar={
              <select
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
              </select>
            }
            onRowClick={(row) => router.push(entityHref("rollouts", row.id))}
            serverSide={{
              rowCount: query.data?.count ?? 0,
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) =>
                setFilters(state ?? "", next.pageIndex),
            }}
          />
        </PageShell>
      </DeliveryProjectGate>
  );
}
export const Route = createFileRoute("/dashboard/delivery/rollouts/")({
  component: function DeliveryRolloutsRedirect() {
    return <RedirectDeliveryList tab="rollouts" />;
  },
});
