import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Eye, Pause, RefreshCw } from "lucide-react";
import { Link } from "@/lib/link";
import { DataTable, type Column } from "@/components/ui/data-table";
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
  deliveryPageRowCount,
  primaryButton,
  secondaryButton,
  textareaClass,
  useDeliveryPageIndex,
  useDeliveryWorkspace,
  withProjectQuery,
} from "@/components/delivery/shared";
import {
  actOnClusterDeployment,
  getClusterDeployment,
  listClusterDeploymentEvents,
  type ClusterDeploymentEvent,
  type DeliveryCondition,
} from "@/lib/api/delivery";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks";
import { can } from "@/lib/permissions";
import { useParams } from "@/lib/navigation";
import { formatRelativeTime } from "@/lib/utils";
import { liveFallback } from "@/lib/live/status-store";
import { useLiveQueryInvalidation } from "@/lib/live/hooks";
import { toastSuccess } from "@/lib/toast";

export function DeploymentDetailPage() {
  const { deploymentId } = useParams<{ deploymentId: string }>();
  const { projectId, projects, projectQuery, setProjectId, listHref } =
    useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_deployments", "read", scope);
  const canUpdate = can(user, "delivery_deployments", "update", scope);
  const [action, setAction] = useState<"reconcile" | "suspend" | null>(null);
  const [diagnostics, setDiagnostics] = useState(false);
  const [eventPage, setEventPage] = useDeliveryPageIndex();
  const pageSize = 20;
  const detail = useQuery({
    queryKey: queryKeys.delivery.deployment(projectId, deploymentId),
    queryFn: () => getClusterDeployment(projectId, deploymentId),
    enabled: Boolean(projectId && deploymentId && allowed),
    refetchInterval: liveFallback(5_000),
  });
  const events = useQuery({
    queryKey: queryKeys.delivery.deploymentEvents(projectId, deploymentId, {
      limit: pageSize,
      offset: eventPage * pageSize,
    }),
    queryFn: () =>
      listClusterDeploymentEvents(projectId, deploymentId, {
        limit: pageSize,
        offset: eventPage * pageSize,
      }),
    enabled: Boolean(projectId && deploymentId && allowed),
    refetchInterval: liveFallback(10_000),
  });
  useLiveQueryInvalidation(
    "cluster_deployment.changed",
    projectId
      ? queryKeys.delivery.deploymentsAll(projectId)
      : queryKeys.delivery.all,
  );
  const deployment = detail.data?.data.deployment;
  const conditionColumns: Column<DeliveryCondition>[] = [
    { key: "type", header: "Condition", accessor: (row) => row.type },
    {
      key: "status",
      header: "Status",
      accessor: (row) => (
        <DeliveryPhaseBadge
          value={
            row.status === "True"
              ? row.type === "Ready"
                ? "ready"
                : row.type.toLowerCase()
              : row.status.toLowerCase()
          }
        />
      ),
    },
    { key: "reason", header: "Reason", accessor: (row) => row.reason || "—" },
    {
      key: "message",
      header: "Sanitized message",
      accessor: (row) => (
        <span className="max-w-xl whitespace-normal">{row.message || "—"}</span>
      ),
    },
    {
      key: "transition",
      header: "Last transition",
      accessor: (row) => formatRelativeTime(row.lastTransitionTime),
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
        permission="delivery_deployments:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <Link
            href={withProjectQuery(listHref("deployments"), projectId)}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" /> Deployments
          </Link>
          <PageHeader
            eyebrow="Cluster deployment"
            title={deployment?.id ?? "Deployment"}
            description={
              deployment
                ? `Target ${deployment.targetId} on cluster ${deployment.clusterId}`
                : "Loading normalized status"
            }
            actions={
              deployment ? (
                <>
                  <button
                    type="button"
                    className={secondaryButton}
                    onClick={() => setDiagnostics(true)}
                  >
                    <Eye className="h-4 w-4" /> Advanced diagnostics
                  </button>
                  <button
                    type="button"
                    className={secondaryButton}
                    disabled={!canUpdate}
                    onClick={() => setAction("suspend")}
                  >
                    <Pause className="h-4 w-4" /> Suspend
                  </button>
                  <button
                    type="button"
                    className={primaryButton}
                    disabled={!canUpdate}
                    onClick={() => setAction("reconcile")}
                  >
                    <RefreshCw className="h-4 w-4" /> Reconcile
                  </button>
                </>
              ) : undefined
            }
          />
          {deployment && (
            <>
              <DetailGrid>
                <Detail
                  label="Phase"
                  value={<DeliveryPhaseBadge value={deployment.phase} />}
                />
                <Detail
                  label="Generation"
                  value={`${deployment.observedGeneration} observed / ${deployment.desiredGeneration} desired`}
                />
                <Detail
                  label="Desired revision"
                  value={deployment.desiredRevision}
                  mono
                />
                <Detail
                  label="Observed revision"
                  value={deployment.observedRevision || "Not observed"}
                  mono
                />
                <Detail
                  label="Source"
                  value={`${deployment.sourceKind} ${deployment.sourceName}`}
                />
                <Detail
                  label="Reconciler"
                  value={`${deployment.reconcilerKind} ${deployment.reconcilerName}`}
                />
                <Detail
                  label="Last observed"
                  value={
                    deployment.lastObservedAt
                      ? new Date(deployment.lastObservedAt).toLocaleString()
                      : "Never"
                  }
                />
                <Detail
                  label="Last error"
                  value={deployment.lastErrorCode || "None"}
                />
              </DetailGrid>
              {deployment.lastMessage && (
                <div className="rounded-md border border-border bg-muted/20 p-3 text-sm">
                  <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
                    Latest sanitized message
                  </p>
                  <p className="mt-1">{deployment.lastMessage}</p>
                </div>
              )}
              <PageSection title="Normalized conditions">
                <DataTable
                  data={deployment.conditions}
                  columns={conditionColumns}
                  keyExtractor={(row) => row.type}
                  searchable={false}
                  emptyMessage="No conditions observed"
                />
              </PageSection>
            </>
          )}
          <PageSection
            title="Event history"
            description="Observed phase changes for this deployment. Historical transitions are complete; they are not in-flight."
          >
            <DataTable
              data={events.data?.data ?? []}
              columns={eventColumns}
              keyExtractor={(row) => row.id}
              searchable={false}
              loading={events.isLoading}
              isError={events.isError}
              onRetry={() => void events.refetch()}
              emptyMessage="No deployment events recorded"
              serverSide={{
                rowCount: deliveryPageRowCount(events.data, eventPage, pageSize),
                pagination: { pageIndex: eventPage, pageSize },
                onPaginationChange: (next) => setEventPage(next.pageIndex),
              }}
            />
          </PageSection>
        </PageShell>
      </DeliveryProjectGate>
      {deployment && action && (
        <DeploymentActionDialog
          projectId={projectId}
          deploymentId={deploymentId}
          action={action}
          etag={detail.data?.etag ?? deployment.desiredGeneration}
          onClose={() => setAction(null)}
        />
      )}
      {deployment && diagnostics && (
        <DiagnosticsDialog
          deployment={deployment}
          onClose={() => setDiagnostics(false)}
        />
      )}
    </DeliveryShell>
  );
}

function DeploymentActionDialog({
  projectId,
  deploymentId,
  action,
  etag,
  onClose,
}: {
  projectId: string;
  deploymentId: string;
  action: "reconcile" | "suspend";
  etag: string | number;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const mutation = useMutation({
    mutationFn: (reason: string) =>
      actOnClusterDeployment(
        projectId,
        deploymentId,
        action,
        etag,
        reason,
        crypto.randomUUID(),
      ),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.deployment(projectId, deploymentId),
      });
      client.invalidateQueries({
        queryKey: queryKeys.delivery.deploymentsAll(projectId),
      });
      toastSuccess(`Deployment ${action} accepted`);
      onClose();
    },
  });
  return (
    <ModalShell
      title={`${action === "reconcile" ? "Reconcile" : "Suspend"} deployment`}
      onClose={onClose}
      subtitle="The request is generation-fenced and does not expose or edit downstream objects."
    >
      <form
        className="space-y-4"
        onSubmit={(event: FormEvent<HTMLFormElement>) => {
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

function DiagnosticsDialog({
  deployment,
  onClose,
}: {
  deployment: NonNullable<
    Awaited<ReturnType<typeof getClusterDeployment>>["data"]["deployment"]
  >;
  onClose: () => void;
}) {
  return (
    <ModalShell
      title="Advanced delivery diagnostics"
      size="xl"
      onClose={onClose}
      subtitle="Sanitized, read-only normalized metadata. Credentials, Secrets, rendered manifests, and arbitrary object editing are intentionally unavailable."
    >
      <div className="space-y-4">
        <DetailGrid>
          <Detail
            label="Desired spec digest"
            value={deployment.desiredSpecDigest}
            mono
          />
          <Detail
            label="Observed spec digest"
            value={deployment.observedSpecDigest}
            mono
          />
          <Detail
            label="Agent session"
            value={deployment.agentSessionId}
            mono
          />
          <Detail label="Agent sequence" value={deployment.agentSequence} />
        </DetailGrid>
        <div>
          <h3 className="mb-2 text-sm font-medium">
            Sanitized inventory summary
          </h3>
          <pre className="max-h-96 overflow-auto rounded-md border border-border bg-muted/30 p-4 text-xs">
            {JSON.stringify(deployment.inventory, null, 2)}
          </pre>
        </div>
      </div>
    </ModalShell>
  );
}

const eventColumns: Column<ClusterDeploymentEvent>[] = [
  {
    key: "observed",
    header: "Observed",
    accessor: (row) => (
      <span className="whitespace-nowrap text-xs text-muted-foreground">
        {new Date(row.observedAt).toLocaleString()}
      </span>
    ),
    sortAccessor: (row) => row.observedAt,
  },
  {
    key: "event",
    header: "Event",
    accessor: (row) => row.eventType.replaceAll("_", " "),
  },
  {
    key: "phase",
    header: "Phase",
    accessor: (row) => (
      <span className="font-mono text-xs">
        {row.fromPhase || "—"} → {row.toPhase || "—"}
      </span>
    ),
  },
  {
    key: "result",
    header: "Result",
    accessor: (row) => (
      <DeliveryPhaseBadge value={row.toPhase || row.eventType} />
    ),
  },
  {
    key: "generation",
    header: "Gen",
    accessor: (row) => (
      <span className="tabular-nums text-xs">{row.generation}</span>
    ),
    sortAccessor: (row) => row.generation,
  },
  {
    key: "message",
    header: "Message",
    accessor: (row) => (
      <span className="max-w-xl whitespace-normal text-xs">
        {row.message || row.reasonCode || "—"}
      </span>
    ),
  },
];

function DeliveryDeploymentDetailRedirect() {
  const { deploymentId } = useParams<{ deploymentId: string }>();
  return (
    <RedirectDeliveryDetail tab="deployments" id={deploymentId}>
      <DeploymentDetailPage />
    </RedirectDeliveryDetail>
  );
}

export const Route = createFileRoute(
  "/dashboard/delivery/deployments/$deploymentId/",
)({ component: DeliveryDeploymentDetailRedirect });
