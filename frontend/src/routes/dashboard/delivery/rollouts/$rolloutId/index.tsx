import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Ban, Check, Pause, Play, RotateCcw, X } from "lucide-react";
import { Link } from "@/lib/link";
import { DataTable, type Column } from "@/components/ui/data-table";
import {
  OperationTimeline,
  type OperationTimelineStepStatus,
} from "@/components/ui/operation-timeline";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  RedirectDeliveryDetail,
  primaryButton,
  secondaryButton,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
  withProjectQuery,
} from "@/components/delivery/shared";
import {
  actOnDeliveryRollout,
  approveDeliveryRollout,
  getDeliveryRollout,
  listDeliveryRolloutClusters,
  listDeliveryRolloutEvents,
  rolloutIsTerminal,
  type DeliveryRolloutCluster,
  type DeliveryRolloutEvent,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useParams } from "@/lib/navigation";
import { formatRelativeTime } from "@/lib/utils";
import { liveFallback } from "@/lib/live/status-store";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { toastSuccess } from "@/lib/toast";

type RolloutAction = "pause" | "resume" | "abort" | "retry" | "rollback";

export function RolloutDetailPage() {
  const { rolloutId } = useParams<{ rolloutId: string }>();
  const { projectId, projects, projectQuery, setProjectId, listHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_rollouts", "read", scope);
  const canUpdate = can(user, "delivery_rollouts", "update", scope);
  const canApprove = can(user, "delivery_approvals", "approve", scope);
  const canRollback = can(user, "delivery_rollbacks", "rollback", scope);
  const [clusterPage, setClusterPage] = useDeliveryPageIndex("cluster_page");
  const [action, setAction] = useState<RolloutAction | null>(null);
  const [approval, setApproval] = useState<{
    cohort: number;
    digest: string;
  } | null>(null);
  const pageSize = 50;
  const detail = useQuery({
    queryKey: queryKeys.delivery.rollout(projectId, rolloutId),
    queryFn: () => getDeliveryRollout(projectId, rolloutId),
    enabled: Boolean(projectId && rolloutId && allowed),
    refetchInterval: (query) =>
      query.state.data && rolloutIsTerminal(query.state.data.data.rollout.state)
        ? liveFallback(30_000)()
        : liveFallback(5_000)(),
  });
  const clusters = useQuery({
    queryKey: queryKeys.delivery.rolloutClusters(projectId, rolloutId, {
      limit: pageSize,
      offset: clusterPage * pageSize,
    }),
    queryFn: () =>
      listDeliveryRolloutClusters(projectId, rolloutId, {
        limit: pageSize,
        offset: clusterPage * pageSize,
      }),
    enabled: Boolean(projectId && rolloutId && allowed),
    refetchInterval: liveFallback(5_000),
  });
  const events = useQuery({
    queryKey: queryKeys.delivery.rolloutEvents(projectId, rolloutId, {
      limit: 100,
    }),
    queryFn: () =>
      listDeliveryRolloutEvents(projectId, rolloutId, { limit: 100 }),
    enabled: Boolean(projectId && rolloutId && allowed),
    refetchInterval: liveFallback(10_000),
  });
  useLiveQueryInvalidation(
    "delivery_rollout.changed",
    projectId
      ? queryKeys.delivery.rolloutsAll(projectId)
      : queryKeys.delivery.all,
  );
  const rollout = detail.data?.data.rollout;
  const plan = detail.data?.data.frozenPlan;
  const availableActions: RolloutAction[] =
    rollout?.state === "paused"
      ? ["resume", "abort", "retry", "rollback"]
      : rollout?.state === "failed" || rollout?.state === "rollback_failed"
        ? ["retry", "rollback"]
        : rollout && !rolloutIsTerminal(rollout.state)
          ? ["pause", "abort"]
          : rollout?.state === "succeeded"
            ? ["rollback"]
            : [];
  const pendingGates = plan
    ? [
        {
          cohort: -1,
          digest: plan.approval.digest,
          required: plan.approval.required,
          name: "Initial approval",
        },
        ...plan.cohorts.map((cohort) => ({
          cohort: cohort.index,
          digest: cohort.approvalDigest ?? "",
          required: cohort.approvalRequired,
          name: cohort.name,
        })),
      ].filter(
        (gate) =>
          gate.required &&
          gate.digest &&
          !detail.data?.data.approvals.some(
            (decision) => decision.cohort === gate.cohort,
          ),
      )
    : [];
  const clusterColumns: Column<DeliveryRolloutCluster>[] = [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => <code className="text-xs">{row.clusterId}</code>,
    },
    {
      key: "cohort",
      header: "Cohort / order",
      accessor: (row) => `${row.cohort} / ${row.releaseOrder}`,
    },
    {
      key: "state",
      header: "State",
      accessor: (row) => <DeliveryPhaseBadge value={row.state} />,
    },
    {
      key: "action",
      header: "Assignment",
      accessor: (row) => row.assignmentAction,
    },
    { key: "attempt", header: "Attempt", accessor: (row) => row.attempt },
    {
      key: "updated",
      header: "Updated",
      accessor: (row) => formatRelativeTime(row.updatedAt),
    },
  ];
  return (
    <DeliveryShell
      projectId={projectId}
      projects={projects}
      setProjectId={setProjectId}
    >
      <DeliveryProjectGate
        projectId={projectId}
        loading={projectQuery.isLoading}
        error={projectQuery.isError}
        projectsCount={projects.length}
        permission="delivery_rollouts:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <Link
            href={withProjectQuery(listHref("rollouts"), projectId)}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" /> Rollouts
          </Link>
          <PageHeader
            eyebrow="Rollout"
            title={rollout?.id ?? "Rollout"}
            description={
              rollout
                ? `${rollout.strategy.type.replaceAll("_", " ")} strategy · immutable plan ${rollout.planDigest}`
                : "Loading frozen rollout"
            }
            actions={
              rollout ? (
                <>
                  {availableActions.map((value) => (
                    <button
                      key={value}
                      type="button"
                      className={secondaryButton}
                      disabled={
                        value === "rollback" ? !canRollback : !canUpdate
                      }
                      onClick={() => setAction(value)}
                    >
                      {actionIcon(value)}
                      {value}
                    </button>
                  ))}
                </>
              ) : undefined
            }
          />
          {rollout && (
            <DetailGrid>
              <Detail
                label="State"
                value={<DeliveryPhaseBadge value={rollout.state} />}
              />
              <Detail
                label="Ready"
                value={`${rollout.readyClusters} / ${rollout.totalClusters}`}
              />
              <Detail
                label="Failed / blocked"
                value={`${rollout.failedClusters} / ${rollout.blockedClusters}`}
              />
              <Detail label="Fence" value={rollout.fencingGeneration} />
              <Detail
                label="Previous version"
                value={rollout.fromBundleVersionId || "No previous known-good"}
                mono
              />
              <Detail
                label="Desired version"
                value={rollout.toBundleVersionId}
                mono
              />
              <Detail
                label="Started"
                value={
                  rollout.startedAt
                    ? new Date(rollout.startedAt).toLocaleString()
                    : "Not started"
                }
              />
              <Detail
                label="Deadline"
                value={
                  rollout.progressDeadline
                    ? new Date(rollout.progressDeadline).toLocaleString()
                    : "Not set"
                }
              />
            </DetailGrid>
          )}
          {pendingGates.length > 0 && (
            <PageSection
              title="Approval gates"
              description="Each decision is bound to this exact frozen digest and expires."
            >
              <div className="space-y-2">
                {pendingGates.map((gate) => (
                  <div
                    key={gate.cohort}
                    className="flex flex-col gap-2 rounded-md border border-status-warning/30 bg-status-warning/10 p-3 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div>
                      <p className="font-medium">{gate.name}</p>
                      <p className="font-mono text-xs text-muted-foreground">
                        {gate.digest}
                      </p>
                    </div>
                    <button
                      type="button"
                      className={primaryButton}
                      disabled={!canApprove}
                      onClick={() =>
                        setApproval({
                          cohort: gate.cohort,
                          digest: gate.digest,
                        })
                      }
                    >
                      Review approval
                    </button>
                  </div>
                ))}
              </div>
            </PageSection>
          )}
          <PageSection
            title="Per-cluster progress"
            description="Clusters are server-paginated; state reflects durable assignment and normalized downstream readiness."
          >
            <DataTable
              data={clusters.data?.data ?? []}
              columns={clusterColumns}
              keyExtractor={(row) => row.id}
              loading={clusters.isLoading}
              isError={clusters.isError}
              onRetry={() => void clusters.refetch()}
              searchable={false}
              emptyMessage="No cluster assignments"
              serverSide={{
                rowCount: clusters.data?.count ?? 0,
                pagination: { pageIndex: clusterPage, pageSize },
                onPaginationChange: (next) => setClusterPage(next.pageIndex),
              }}
            />
          </PageSection>
          <PageSection title="Timeline">
            <OperationTimeline
              header="Rollout events"
              headerMeta={`${events.data?.count ?? 0} events`}
              steps={(events.data?.data ?? []).map(eventStep)}
            />
          </PageSection>
        </PageShell>
      </DeliveryProjectGate>
      {rollout && action && (
        <RolloutActionDialog
          projectId={projectId}
          rolloutId={rolloutId}
          etag={detail.data?.etag ?? rollout.fencingGeneration}
          action={action}
          onClose={() => setAction(null)}
        />
      )}
      {rollout && approval && (
        <ApprovalDialog
          projectId={projectId}
          rolloutId={rolloutId}
          etag={detail.data?.etag ?? rollout.fencingGeneration}
          gate={approval}
          onClose={() => setApproval(null)}
        />
      )}
    </DeliveryShell>
  );
}

function RolloutActionDialog({
  projectId,
  rolloutId,
  etag,
  action,
  onClose,
}: {
  projectId: string;
  rolloutId: string;
  etag: string | number;
  action: RolloutAction;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (reason: string) =>
      actOnDeliveryRollout(
        projectId,
        rolloutId,
        action,
        etag,
        reason,
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.rollout(projectId, rolloutId),
      });
      client.invalidateQueries({
        queryKey: queryKeys.delivery.rolloutsAll(projectId),
      });
      toastSuccess(`Rollout ${action} accepted`);
      onClose();
    },
  });
  return (
    <ModalShell
      title={`${action.charAt(0).toUpperCase()}${action.slice(1)} rollout`}
      onClose={onClose}
      subtitle="This is a compare-and-swap action against the current fencing generation."
    >
      <form
        className="space-y-4"
        onSubmit={(event) => {
          event.preventDefault();
          mutation.mutate(
            String(
              new FormData(event.currentTarget).get("reason") ?? "",
            ).trim(),
          );
        }}
      >
        <label className="block space-y-1.5 text-sm">
          <span className="font-medium">Audit reason code</span>
          <textarea
            name="reason"
            required
            maxLength={96}
            className={textareaClass}
            placeholder={`${action}_requested`}
          />
        </label>
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={mutation.isPending}
          >
            Confirm {action}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function ApprovalDialog({
  projectId,
  rolloutId,
  etag,
  gate,
  onClose,
}: {
  projectId: string;
  rolloutId: string;
  etag: string | number;
  gate: { cohort: number; digest: string };
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (decision: "approved" | "rejected") =>
      approveDeliveryRollout(
        rolloutId,
        {
          project_id: projectId,
          cohort: gate.cohort,
          binding_digest: gate.digest,
          decision,
          expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
        },
        etag,
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.rollout(projectId, rolloutId),
      });
      toastSuccess("Approval decision recorded");
      onClose();
    },
  });
  return (
    <ModalShell
      title="Review rollout approval"
      onClose={onClose}
      subtitle="Decision expires in 30 minutes and applies only to this binding digest."
    >
      <div className="space-y-4">
        <DetailGrid>
          <Detail
            label="Cohort"
            value={gate.cohort === -1 ? "Initial gate" : gate.cohort}
          />
          <Detail label="Binding digest" value={gate.digest} mono />
        </DetailGrid>
        <p className="text-sm text-muted-foreground">
          Approval releases the frozen cohort subject to availability, failure,
          maintenance-window, and concurrency budgets. Rejection transitions the
          initial gate to rejected or pauses a later cohort.
        </p>
        {mutation.isError && <ErrorMessage error={mutation.error} />}
        <div className="flex justify-end gap-2">
          <button
            type="button"
            className={secondaryButton}
            disabled={mutation.isPending}
            onClick={() => mutation.mutate("rejected")}
          >
            <X className="h-4 w-4" /> Reject
          </button>
          <button
            type="button"
            className={primaryButton}
            disabled={mutation.isPending}
            onClick={() => mutation.mutate("approved")}
          >
            <Check className="h-4 w-4" /> Approve exact digest
          </button>
        </div>
      </div>
    </ModalShell>
  );
}

function eventStep(event: DeliveryRolloutEvent) {
  const failed =
    event.toState?.includes("failed") || event.eventType.includes("failed");
  const status: OperationTimelineStepStatus = failed
    ? "failed"
    : event.toState === "succeeded" || event.toState === "ready"
      ? "success"
      : "running";
  return {
    id: event.id,
    label: event.eventType.replaceAll("_", " "),
    status,
    detail: `${event.fromState || "—"} → ${event.toState || "—"} · ${new Date(event.occurredAt).toLocaleString()}`,
    error: failed ? event.reasonCode : undefined,
  };
}
function actionIcon(action: RolloutAction) {
  if (action === "pause") return <Pause className="h-4 w-4" />;
  if (action === "resume") return <Play className="h-4 w-4" />;
  if (action === "abort") return <Ban className="h-4 w-4" />;
  return <RotateCcw className="h-4 w-4" />;
}
function DeliveryRolloutDetailRedirect() {
  const { rolloutId } = useParams<{ rolloutId: string }>();
  return (
    <RedirectDeliveryDetail tab="rollouts" id={rolloutId}>
      <RolloutDetailPage />
    </RedirectDeliveryDetail>
  );
}

export const Route = createFileRoute(
  "/dashboard/delivery/rollouts/$rolloutId/",
)({ component: DeliveryRolloutDetailRedirect });
