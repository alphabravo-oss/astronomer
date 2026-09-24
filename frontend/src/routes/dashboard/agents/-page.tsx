import { agentColumns } from "./-columns";
import { AgentDiagnosticsDrawer } from "./-diagnostics";
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Server, Unplug } from "lucide-react";
import { DataTable } from "@/components/ui/data-table";
import { EmptyState, PermissionState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { PageHeader, PageShell } from "@/components/ui/page";
import { MetricCard } from "@/components/ui/metric-card";
import {
  createAgentUpgradeOperation,
  createAgentUpgradePlan,
  downloadAgentDiagnosticsBundle,
  getAgentDiagnostics,
  getClusterAgents,
  getAgentOperations,
  runAgentSelfTest,
} from "@/lib/api/cluster-agents";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";
import { formatRelativeTime, downloadBlob } from "@/lib/utils";
import { pageTableCount } from "@/lib/api/pagination";
import { useSearchParam } from "@/lib/use-search-param";
import type { AgentUpgradePlanResponse } from "@/types";

export function ClusterAgentsPage() {
  const { data: user } = useCurrentUser();
  const canRead = can(user, "cluster_agents", "read");
  const canManage = can(user, "cluster_agents", "update");
  const [selectedClusterId, setSelectedClusterId] = useState<string | null>(
    null,
  );
  const [upgradePlan, setUpgradePlan] =
    useState<AgentUpgradePlanResponse | null>(null);
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useSearchParam("q");
  const pageSize = 50;
  const agentsQuery = useQuery({
    queryKey: queryKeys.agents.list({
      limit: pageSize,
      offset: pageIndex * pageSize,
      search,
    }),
    queryFn: ({ signal }) =>
      getClusterAgents(
        { limit: pageSize, offset: pageIndex * pageSize, search },
        { signal },
      ),
    enabled: canRead,
    refetchInterval: liveFallback(30000),
  });

  useLiveQueryInvalidation(
    [
      "cluster.connected",
      "cluster.disconnected",
      "cluster.heartbeat",
      "agent.reconnecting",
      "agent.failed",
      "cluster_agents.changed",
    ],
    [queryKeys.agents.all],
  );

  const items = agentsQuery.data?.data ?? [];
  const summary = agentsQuery.data?.summary;
  const versionEntries = Object.entries(summary?.versions ?? {}).sort(
    (a, b) => b[1] - a[1],
  );
  const compatibilityEntries = Object.entries(
    summary?.compatibility ?? {},
  ).sort((a, b) => b[1] - a[1]);
  const diagnostics = useQuery({
    queryKey: queryKeys.agents.diagnostics(selectedClusterId),
    queryFn: ({ signal }) =>
      getAgentDiagnostics(selectedClusterId!, { signal }),
    enabled: !!selectedClusterId,
    throwOnError: false,
  });
  const operations = useQuery({
    queryKey: queryKeys.agents.operations(selectedClusterId),
    queryFn: ({ signal }) =>
      getAgentOperations(selectedClusterId!, { limit: 10 }, { signal }),
    enabled: !!selectedClusterId,
    throwOnError: false,
    refetchInterval: selectedClusterId ? liveFallback(15000) : false,
  });

  const columns = agentColumns(setSelectedClusterId, setUpgradePlan);

  if (!canRead) return <PermissionState permission="cluster_agents:read" />;

  return (
    <PageShell>
      <PageHeader
        title="Cluster Agents"
        description="Connected, degraded, and disconnected Astronomer agents across adopted clusters."
        actions={
          <div className="text-xs text-muted-foreground max-w-md text-right">
            Server {summary?.serverVersion || "-"} · Supported agent{" "}
            {summary?.minimumSupportedAgentVersion || "-"} · Compatible{" "}
            {summary?.minimumCompatibleAgentVersion || "-"} · Generated{" "}
            {summary?.generatedAt
              ? formatRelativeTime(summary.generatedAt)
              : "-"}
          </div>
        }
      />

      <QueryStates
        query={agentsQuery}
        loadingTitle="Loading cluster agents"
        permission="cluster_agents:read"
        errorTitle="Failed to load cluster agents"
        isEmpty={(result) =>
          result.data.length === 0 && !search && pageIndex === 0
        }
        empty={
          <EmptyState
            icon={Server}
            title="No cluster agents connected"
            description="Register an existing Kubernetes cluster to install its agent and begin reporting health."
            actionLabel="Register cluster"
            actionHref="/dashboard/clusters/register"
          />
        }
      >
        <>
          <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
            <MetricCard
              dense
              icon={<Server className="h-4 w-4" />}
              label="Clusters"
              value={summary?.totalClusters ?? 0}
            />
            <MetricCard
              dense
              icon={<CheckCircle2 className="h-4 w-4" />}
              label="Connected (this page)"
              value={summary?.connected ?? 0}
              tone="success"
            />
            <MetricCard
              dense
              icon={<AlertTriangle className="h-4 w-4" />}
              label="Degraded (this page)"
              value={summary?.degraded ?? 0}
              tone="warning"
            />
            <MetricCard
              dense
              icon={<Unplug className="h-4 w-4" />}
              label="Disconnected (this page)"
              value={summary?.disconnected ?? 0}
            />
          </div>

          <div className="grid gap-4 lg:grid-cols-2">
            <DistributionPanel
              title="Versions (this page)"
              entries={versionEntries}
              empty="No agent versions reported"
            />
            <DistributionPanel
              title="Compatibility (this page)"
              entries={compatibilityEntries}
              empty="No compatibility data"
            />
          </div>

          <DataTable
            data={items}
            columns={columns}
            keyExtractor={(row) => row.clusterId}
            searchPlaceholder="Search by cluster name…"
            emptyState={{
              title: "No agents available",
              description:
                "Resources will appear here when they are available in this scope.",
            }}
            pageSize={pageSize}
            serverSide={{
              ...pageTableCount(agentsQuery.data),
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
              search: {
                value: search,
                onChange: (v) => {
                  setSearch(v);
                  setPageIndex(0);
                },
              },
            }}
          />
        </>
      </QueryStates>
      {selectedClusterId && (
        <AgentDiagnosticsDrawer
          diagnosticsQuery={diagnostics}
          upgradePlan={upgradePlan}
          operationsQuery={operations}
          canManage={canManage}
          onClose={() => {
            setSelectedClusterId(null);
            setUpgradePlan(null);
          }}
          onPlan={async () => {
            const plan = await createAgentUpgradePlan(selectedClusterId);
            setUpgradePlan(plan);
          }}
          onSelfTest={() => runAgentSelfTest(selectedClusterId)}
          onQueue={async () => {
            const result = await createAgentUpgradeOperation(selectedClusterId);
            setUpgradePlan(result.plan);
            await operations.refetch();
            return result;
          }}
          onDownload={async () => {
            const blob =
              await downloadAgentDiagnosticsBundle(selectedClusterId);
            downloadBlob(
              blob,
              `astronomer-agent-diagnostics-${selectedClusterId}.json`,
            );
          }}
        />
      )}
    </PageShell>
  );
}

function DistributionPanel({
  title,
  entries,
  empty,
}: {
  title: string;
  entries: Array<[string, number]>;
  empty: string;
}) {
  return (
    <div className="rounded-md border border-border bg-card p-4">
      <h2 className="text-sm font-medium text-foreground">{title}</h2>
      {entries.length === 0 ? (
        <p className="mt-3 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <div className="mt-3 space-y-2">
          {entries.map(([name, count]) => (
            <div
              key={name}
              className="flex items-center justify-between gap-3 text-sm"
            >
              <span className="truncate font-mono text-xs text-muted-foreground">
                {name}
              </span>
              <span className="tabular-nums text-foreground">{count}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
