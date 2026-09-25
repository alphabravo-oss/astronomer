import { pageTableCount } from "@/lib/api/pagination";
import { lazy, Suspense, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Crosshair, Plus } from "lucide-react";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
import { ActionButton } from "@/components/ui/action-button";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
} from "@/components/delivery/shared";
import {
  listDeliveryTargets,
  type DeliveryTarget,
} from "@/lib/api/delivery-targets";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can } from "@/lib/permissions";
import { useNavigate } from "@tanstack/react-router";
import { formatRelativeTime } from "@/lib/utils";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { liveFallback } from "@/lib/live/status-store";

const CreateTargetDialog = lazy(() => import("./-create-target-dialog"));

export function TargetsPage() {
  const { projectId, projects, projectQuery, entityHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_targets", "list", scope);
  const canCreate = can(user, "delivery_targets", "create", scope);
  const [pageIndex, setPageIndex] = useDeliveryPageIndex();
  const [creating, setCreating] = useState(false);
  const pageSize = 25;
  const params = { limit: pageSize, offset: pageIndex * pageSize };
  const navigate = useNavigate();
  const query = useQuery({
    queryKey: queryKeys.delivery.targets(projectId, params),
    queryFn: ({ signal }) => {
      signal.throwIfAborted();
      return listDeliveryTargets(projectId, params, signal);
    },
    enabled: Boolean(projectId && allowed),
    refetchInterval: liveFallback(20_000),
  });
  useLiveQueryInvalidation(
    "delivery_target.changed",
    projectId
      ? queryKeys.delivery.targetsAll(projectId)
      : queryKeys.delivery.all,
  );
  const columns: Column<DeliveryTarget>[] = [
    {
      key: "name",
      header: "Target",
      accessor: (row) => (
        <div className="flex items-center gap-2">
          <Crosshair className="h-4 w-4 text-muted-foreground" />
          <div>
            <p className="font-medium">{row.name}</p>
            <p className="text-xs text-muted-foreground">
              generation {row.generation}
            </p>
          </div>
        </div>
      ),
    },
    {
      key: "bundle",
      header: "Bundle version",
      accessor: (row) => <code className="text-xs">{row.bundleVersionId}</code>,
    },
    {
      key: "placement",
      header: "Placement",
      accessor: (row) =>
        row.placement.allClusters ? (
          <span className="text-status-warning">All project clusters</span>
        ) : (
          `${row.placement.clusterIds?.length ?? 0} explicit · ${Object.keys(row.placement.matchLabels ?? {}).length} labels · ${row.placement.clusterGroupIds?.length ?? 0} groups`
        ),
    },
    {
      key: "approval",
      header: "Approval",
      accessor: (row) =>
        row.rolloutPolicy.approvalRequired ? "Required" : "Policy controlled",
    },
    {
      key: "state",
      header: "State",
      accessor: (row) => (
        <DeliveryPhaseBadge
          value={
            row.deletionState !== "active"
              ? row.deletionState
              : row.suspended
                ? "suspended"
                : "active"
          }
        />
      ),
    },
    {
      key: "updated",
      header: "Updated",
      accessor: (row) => formatRelativeTime(row.updatedAt),
    },
  ];
  return (
    <>
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_targets:list"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <PageHeader
            title="Targets"
            description="Bind an immutable bundle version to centrally evaluated placement and rollout policy."
            actions={
              canCreate ? (
                <ActionButton
                  onClick={() => setCreating(true)}
                  intent="primary"
                  icon={<Plus className="h-4 w-4" />}
                >
                  New target
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
            permission="delivery_targets:list"
            onRetry={() => void query.refetch()}
            searchable={false}
            emptyState={{
              title: "No delivery targets in this project",
              description:
                "Resources will appear here when they are available in this scope.",
            }}
            onRowClick={(row) =>
              void navigate({ to: entityHref("targets", row.id) })
            }
            serverSide={{
              ...pageTableCount(query.data),
              pagination: { pageIndex, pageSize },
              onPaginationChange: (next) => setPageIndex(next.pageIndex),
            }}
          />
        </PageShell>
      </DeliveryProjectGate>
      {creating && (
        <Suspense fallback={<div role="status">Loading target form…</div>}>
          <CreateTargetDialog
            key={projectId}
            projectId={projectId}
            onClose={() => setCreating(false)}
          />
        </Suspense>
      )}
    </>
  );
}
