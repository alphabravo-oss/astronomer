import { createFileRoute } from "@tanstack/react-router";
import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  Eye,
  Pause,
  Pencil,
  Play,
  Rocket,
  Trash2,
} from "lucide-react";
import { Link } from "@/lib/link";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { ModalShell } from "@/components/ui/modal-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  dangerButton,
  inputClass,
  primaryButton,
  secondaryButton,
  textareaClass,
  RedirectDeliveryDetail,
  useDeliveryWorkspace,
  withProjectQuery,
} from "@/components/delivery/shared";
import {
  deleteDeliveryTarget,
  getDeliveryTarget,
  listDeliveryConfigurationTemplates,
  listDeliveryOverrideSets,
  listClusterDeployments,
  listDeliveryRollouts,
  orphanDeliveryTarget,
  previewDeliveryTarget,
  startDeliveryRollout,
  updateDeliveryTarget,
  type AmountType,
  type DeliveryTarget,
  type ClusterDeployment,
  type DeliveryRollout,
  type DriftPolicy,
  type PlacementDecision,
  type PlacementPreview,
  type RolloutFailureAction,
  type RolloutStrategyRequest,
  type RolloutStrategyType,
} from "@/lib/api/delivery";
import {
  placementFormDefaults,
  placementFromForm,
  placementHasSelector,
} from "@/components/delivery/target-form";
import { queryKeys } from "@/lib/query-keys";
import { useAuditLogs, useClusters, useCurrentUser } from "@/lib/hooks";
import { can, isSuperuser } from "@/lib/permissions";
import { useParams, useRouter } from "@/lib/navigation";
import { toastSuccess } from "@/lib/toast";
import { formatRelativeTime } from "@/lib/utils";
import type { AuditLogEntry } from "@/types";

export function TargetDetailPage() {
  const { targetId } = useParams<{ targetId: string }>();
  const {
    projectId,
    projects,
    projectQuery,
    setProjectId,
    listHref,
    entityHref,
  } = useDeliveryWorkspace();
  const { data: user } = useCurrentUser();
  const scope = { type: "project" as const, id: projectId };
  const allowed = can(user, "delivery_targets", "read", scope);
  const canUpdate = can(user, "delivery_targets", "update", scope);
  const canDelete = can(user, "delivery_targets", "delete", scope);
  const canRollout = can(user, "delivery_rollouts", "create", scope);
  const canOrphan =
    isSuperuser(user) && can(user, "delivery_orphans", "orphan", scope);
  const [preview, setPreview] = useState<PlacementPreview | null>(null);
  const [previewCursors, setPreviewCursors] = useState<string[]>([""]);
  const [previewPageIndex, setPreviewPageIndex] = useState(0);
  const [launching, setLaunching] = useState(false);
  const [editing, setEditing] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [orphaning, setOrphaning] = useState(false);
  const client = useQueryClient();
  const router = useRouter();
  const query = useQuery({
    queryKey: queryKeys.delivery.target(projectId, targetId),
    queryFn: ({ signal }) => getDeliveryTarget(projectId, targetId, signal),
    enabled: Boolean(projectId && targetId && allowed),
  });
  const deployments = useQuery({
    queryKey: queryKeys.delivery.deployments(projectId, {
      target_id: targetId,
      limit: 200,
    }),
    queryFn: ({ signal }) =>
      listClusterDeployments(
        projectId,
        { target_id: targetId, limit: 200 },
        signal,
      ),
    enabled: Boolean(projectId && targetId && allowed),
  });
  const rollouts = useQuery({
    queryKey: queryKeys.delivery.rollouts(projectId, {
      target_id: targetId,
      limit: 25,
    }),
    queryFn: ({ signal }) =>
      listDeliveryRollouts(
        projectId,
        { target_id: targetId, limit: 25 },
        signal,
      ),
    enabled: Boolean(projectId && targetId && allowed),
  });
  const clusterQuery = useClusters({ pageSize: 200 });
  const audit = useAuditLogs(
    {
      resource_type: "delivery_target",
      resource_id: targetId,
      project_id: projectId,
      limit: 50,
    },
    { enabled: Boolean(projectId && targetId && allowed) },
  );
  const clusterNames = new Map(
    (clusterQuery.data?.data ?? []).map((cluster) => [
      cluster.id,
      cluster.displayName || cluster.name,
    ]),
  );
  const previewMutation = useMutation({
    mutationFn: ({
      cursor,
    }: {
      cursor: string;
      pageIndex: number;
      reset?: boolean;
    }) =>
      previewDeliveryTarget(projectId, targetId, {
        pageSize: 100,
        cursor: cursor || undefined,
      }),
    onSuccess: (nextPreview, request) => {
      setPreview(nextPreview);
      setPreviewPageIndex(request.pageIndex);
      if (request.reset) {
        setPreviewCursors([""]);
      } else {
        setPreviewCursors((current) => {
          if (current[request.pageIndex] === request.cursor) return current;
          const next = current.slice(0, request.pageIndex);
          next[request.pageIndex] = request.cursor;
          return next;
        });
      }
    },
  });
  const suspendMutation = useMutation({
    mutationFn: () => {
      if (!query.data) throw new Error("Target is not loaded.");
      return updateDeliveryTarget(
        targetId,
        { project_id: projectId, suspended: !query.data.data.suspended },
        query.data.etag ?? query.data.data.resourceVersion,
        crypto.randomUUID(),
      );
    },
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.target(projectId, targetId),
      });
      toastSuccess(
        query.data?.data.suspended ? "Target resumed" : "Target suspended",
      );
    },
  });
  const deleteMutation = useMutation({
    mutationFn: () => {
      if (!query.data) throw new Error("Target is not loaded.");
      return deleteDeliveryTarget(
        projectId,
        targetId,
        query.data.etag ?? query.data.data.resourceVersion,
        crypto.randomUUID(),
      );
    },
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.targetsAll(projectId),
      });
      toastSuccess("Target deletion started");
      router.push(withProjectQuery(listHref("targets"), projectId));
    },
  });
  const orphanMutation = useMutation({
    mutationFn: () => {
      if (!query.data) throw new Error("Target is not loaded.");
      return orphanDeliveryTarget(
        projectId,
        targetId,
        query.data.etag ?? query.data.data.resourceVersion,
        crypto.randomUUID(),
      );
    },
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.targetsAll(projectId),
      });
      toastSuccess("Target marked orphaned");
      router.push(withProjectQuery(listHref("targets"), projectId));
    },
  });
  const target = query.data?.data;
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
        permission="delivery_targets:read"
        allowed={allowed}
        onRetry={() => void projectQuery.refetch()}
      >
        <PageShell>
          <Link
            href={withProjectQuery(listHref("targets"), projectId)}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" /> Targets
          </Link>
          <PageHeader
            eyebrow="Delivery target"
            title={target?.name ?? "Target"}
            description={target?.description || "Placement and rollout policy"}
            actions={
              target ? (
                <>
                  <button
                    type="button"
                    className={secondaryButton}
                    disabled={!canUpdate}
                    onClick={() => setEditing(true)}
                  >
                    <Pencil className="h-4 w-4" /> Edit configuration
                  </button>
                  <button
                    type="button"
                    className={secondaryButton}
                    disabled={!canUpdate || suspendMutation.isPending}
                    onClick={() => suspendMutation.mutate()}
                  >
                    {target.suspended ? (
                      <Play className="h-4 w-4" />
                    ) : (
                      <Pause className="h-4 w-4" />
                    )}
                    {target.suspended ? "Resume target" : "Suspend target"}
                  </button>
                  <button
                    type="button"
                    className={primaryButton}
                    disabled={previewMutation.isPending}
                    onClick={() =>
                      previewMutation.mutate({
                        cursor: "",
                        pageIndex: 0,
                        reset: true,
                      })
                    }
                  >
                    <Eye className="h-4 w-4" />{" "}
                    {previewMutation.isPending
                      ? "Evaluating…"
                      : "Preview placement"}
                  </button>
                  {canDelete && (
                    <button
                      type="button"
                      className={dangerButton}
                      onClick={() => setDeleting(true)}
                    >
                      <Trash2 className="h-4 w-4" /> Delete
                    </button>
                  )}
                </>
              ) : undefined
            }
          />
          {target && (
            <>
              <DetailGrid>
                <Detail
                  label="State"
                  value={
                    <DeliveryPhaseBadge
                      value={
                        target.deletionState !== "active"
                          ? target.deletionState
                          : target.suspended
                            ? "suspended"
                            : "active"
                      }
                    />
                  }
                />
                <Detail label="Generation" value={target.generation} />
                <Detail
                  label="Bundle version"
                  value={target.bundleVersionId}
                  mono
                />
                <Detail
                  label="Approval"
                  value={
                    target.rolloutPolicy.approvalRequired
                      ? "Required"
                      : "Policy controlled"
                  }
                />
                <Detail
                  label="Reconcile interval"
                  value={target.reconciliationPolicy.interval}
                />
                <Detail
                  label="Drift policy"
                  value={target.reconciliationPolicy.drift}
                />
                <Detail
                  label="Configuration template"
                  value={target.configurationTemplateId ?? "Bundle defaults"}
                  mono={Boolean(target.configurationTemplateId)}
                />
                <Detail
                  label="Override layers"
                  value={target.overrideSetIds.length}
                />
              </DetailGrid>
              <PageSection title="Placement intent">
                <pre className="max-h-80 overflow-auto rounded-lg border border-border bg-muted/30 p-4 text-xs">
                  {JSON.stringify(target.placement, null, 2)}
                </pre>
              </PageSection>
              <ApplicationStatus
                target={target}
                deployments={deployments.data?.data ?? []}
              />
              <PageSection
                title="Cluster deployments"
                description="Desired and observed state for every cluster selected by this application target."
              >
                <DataTable
                  data={deployments.data?.data ?? []}
                  columns={deploymentColumns(clusterNames)}
                  keyExtractor={(row) => row.id}
                  searchable={false}
                  loading={deployments.isLoading}
                  isError={deployments.isError}
                  onRetry={() => void deployments.refetch()}
                  emptyMessage="This application has no cluster deployments yet"
                  onRowClick={(row) =>
                    router.push(entityHref("deployments", row.id))
                  }
                />
              </PageSection>
              <PageSection
                title="Release history"
                description="Immutable rollout plans for this application, newest first."
              >
                <DataTable
                  data={rollouts.data?.data ?? []}
                  columns={rolloutColumns}
                  keyExtractor={(row) => row.id}
                  searchable={false}
                  loading={rollouts.isLoading}
                  isError={rollouts.isError}
                  onRetry={() => void rollouts.refetch()}
                  emptyMessage="No rollouts have been launched"
                  onRowClick={(row) =>
                    router.push(entityHref("rollouts", row.id))
                  }
                />
              </PageSection>
              <PageSection
                title="Audit history"
                description="Who changed this application target and when. Rollout and per-cluster events remain in their operational timelines."
              >
                <DataTable
                  data={audit.data?.data ?? []}
                  columns={auditColumns}
                  keyExtractor={(row) => row.id}
                  searchable={false}
                  loading={audit.isLoading}
                  isError={audit.isError}
                  onRetry={() => void audit.refetch()}
                  emptyMessage="No audited target changes are visible"
                />
              </PageSection>
            </>
          )}
          {previewMutation.isError && (
            <ErrorMessage error={previewMutation.error} />
          )}
          {preview && (
            <PreviewPanel
              preview={preview}
              canLaunch={canRollout && !target?.suspended}
              onLaunch={() => setLaunching(true)}
              pageIndex={previewPageIndex}
              loadingPage={previewMutation.isPending}
              canGoBack={previewPageIndex > 0}
              onPrevious={() => {
                const destination = previewPageIndex - 1;
                previewMutation.mutate({
                  cursor: previewCursors[destination] ?? "",
                  pageIndex: destination,
                });
              }}
              onNext={() => {
                if (!preview.nextCursor) return;
                previewMutation.mutate({
                  cursor: preview.nextCursor,
                  pageIndex: previewPageIndex + 1,
                });
              }}
            />
          )}
        </PageShell>
      </DeliveryProjectGate>
      {target && preview && launching && (
        <LaunchDialog
          projectId={projectId}
          target={target}
          preview={preview}
          onClose={() => setLaunching(false)}
        />
      )}
      {target && query.data && editing && (
        <TargetEditDialog
          projectId={projectId}
          target={target}
          etag={query.data.etag ?? target.resourceVersion}
          onUpdated={() => setPreview(null)}
          onClose={() => setEditing(false)}
        />
      )}
      <ConfirmDialog
        open={deleting}
        onClose={() => setDeleting(false)}
        onConfirm={() => deleteMutation.mutate()}
        title="Delete delivery target"
        description="Astronomer will create deletion tombstones and wait for downstream prune/uninstall policy. Disconnected clusters remain visible for follow-up."
        confirmValue={target?.name}
        variant="destructive"
        loading={deleteMutation.isPending}
      >
        {canOrphan && (
          <button
            type="button"
            className={secondaryButton}
            onClick={() => {
              setDeleting(false);
              setOrphaning(true);
            }}
          >
            Orphan workloads instead
          </button>
        )}
      </ConfirmDialog>
      <ConfirmDialog
        open={orphaning}
        onClose={() => setOrphaning(false)}
        onConfirm={() => orphanMutation.mutate()}
        title="Orphan managed workloads"
        description="Stop managing these workloads without deleting them. This is a privileged break-glass action and is audited."
        confirmValue="ORPHAN"
        confirmText="Orphan"
        variant="destructive"
        loading={orphanMutation.isPending}
      />
    </DeliveryShell>
  );
}

function ApplicationStatus({
  target,
  deployments,
}: {
  target: DeliveryTarget;
  deployments: ClusterDeployment[];
}) {
  const ready = deployments.filter((row) => row.phase === "ready").length;
  const drifted = deployments.filter((row) =>
    row.conditions.some(
      (condition) =>
        condition.type === "Drifted" && condition.status === "True",
    ),
  ).length;
  const failed = deployments.filter(
    (row) => row.phase === "failed" || row.phase === "degraded",
  ).length;
  const lastObserved = deployments
    .map((row) => row.lastObservedAt)
    .filter((value): value is string => Boolean(value))
    .sort()
    .at(-1);
  return (
    <PageSection
      title="Application overview"
      description="A target is the durable application identity; each selected cluster has its own observed deployment."
    >
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-5">
        <Metric
          label="Ready / desired"
          value={`${ready} / ${deployments.length}`}
        />
        <Metric label="Failed / degraded" value={failed} />
        <Metric label="Drifted" value={drifted} />
        <Metric label="Target generation" value={target.generation} />
        <Metric
          label="Last observation"
          value={lastObserved ? formatRelativeTime(lastObserved) : "Never"}
        />
      </div>
    </PageSection>
  );
}

function deploymentColumns(
  clusterNames: Map<string, string>,
): Column<ClusterDeployment>[] {
  return [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <div>
          <p className="font-medium">
            {clusterNames.get(row.clusterId) || row.clusterId}
          </p>
          {clusterNames.has(row.clusterId) && (
            <p className="font-mono text-xs text-muted-foreground">
              {row.clusterId}
            </p>
          )}
        </div>
      ),
      sortAccessor: (row) => clusterNames.get(row.clusterId) || row.clusterId,
    },
    {
      key: "phase",
      header: "Phase",
      accessor: (row) => <DeliveryPhaseBadge value={row.phase} />,
      sortAccessor: (row) => row.phase,
    },
    {
      key: "revision",
      header: "Observed revision",
      accessor: (row) => (
        <div>
          <p className="max-w-64 truncate font-mono text-xs">
            {row.observedRevision || "Not observed"}
          </p>
          <p className="text-xs text-muted-foreground">
            generation {row.observedGeneration}/{row.desiredGeneration}
          </p>
        </div>
      ),
    },
    {
      key: "drift",
      header: "Drift",
      accessor: (row) =>
        row.conditions.some(
          (condition) =>
            condition.type === "Drifted" && condition.status === "True",
        ) ? (
          <DeliveryPhaseBadge value="drifted" />
        ) : (
          "In sync"
        ),
    },
    {
      key: "inventory",
      header: "Resources",
      accessor: (row) => {
        const inventory = row.inventory as {
          entries?: number;
          ready?: number;
          failed?: number;
        };
        return `${inventory.ready ?? 0} / ${inventory.entries ?? 0} ready`;
      },
    },
    {
      key: "observed",
      header: "Last observed",
      accessor: (row) =>
        row.lastObservedAt ? formatRelativeTime(row.lastObservedAt) : "Never",
      sortAccessor: (row) => row.lastObservedAt || "",
    },
  ];
}

const rolloutColumns: Column<DeliveryRollout>[] = [
  {
    key: "state",
    header: "State",
    accessor: (row) => <DeliveryPhaseBadge value={row.state} />,
    sortAccessor: (row) => row.state,
  },
  {
    key: "version",
    header: "Version transition",
    accessor: (row) => (
      <div className="font-mono text-xs">
        <p>{row.fromBundleVersionId?.slice(0, 12) || "initial install"}</p>
        <p className="text-muted-foreground">
          → {row.toBundleVersionId.slice(0, 12)}
        </p>
      </div>
    ),
  },
  {
    key: "strategy",
    header: "Strategy",
    accessor: (row) => row.strategy.type.replaceAll("_", " "),
  },
  {
    key: "progress",
    header: "Ready / desired",
    accessor: (row) => `${row.readyClusters} / ${row.totalClusters}`,
    sortAccessor: (row) => row.readyClusters,
  },
  {
    key: "failures",
    header: "Failed / blocked",
    accessor: (row) => `${row.failedClusters} / ${row.blockedClusters}`,
  },
  {
    key: "created",
    header: "Created",
    accessor: (row) => formatRelativeTime(row.createdAt),
    sortAccessor: (row) => row.createdAt,
  },
];

const auditColumns: Column<AuditLogEntry>[] = [
  {
    key: "time",
    header: "Time",
    accessor: (row) =>
      row.timestamp ? new Date(row.timestamp).toLocaleString() : "—",
    sortAccessor: (row) => row.timestamp,
  },
  {
    key: "action",
    header: "Action",
    accessor: (row) => row.action.replaceAll(".", " "),
    sortAccessor: (row) => row.action,
  },
  {
    key: "actor",
    header: "Actor",
    accessor: (row) => row.user || row.userId || "system",
  },
  {
    key: "result",
    header: "Result",
    accessor: (row) => <DeliveryPhaseBadge value={row.status} />,
  },
  {
    key: "request",
    header: "Request",
    accessor: (row) => (
      <span className="font-mono text-xs text-muted-foreground">
        {row.requestId || row.correlationId || "—"}
      </span>
    ),
  },
];

function PreviewPanel({
  preview,
  canLaunch,
  onLaunch,
  pageIndex,
  loadingPage,
  canGoBack,
  onPrevious,
  onNext,
}: {
  preview: PlacementPreview;
  canLaunch: boolean;
  onLaunch: () => void;
  pageIndex: number;
  loadingPage: boolean;
  canGoBack: boolean;
  onPrevious: () => void;
  onNext: () => void;
}) {
  const columns: Column<PlacementDecision>[] = [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <div>
          <p className="font-medium">{row.clusterName || row.clusterId}</p>
          <p className="font-mono text-xs text-muted-foreground">
            {row.clusterId}
          </p>
        </div>
      ),
    },
    {
      key: "decision",
      header: "Decision",
      accessor: (row) => <DeliveryPhaseBadge value={row.reason} />,
    },
    {
      key: "reason",
      header: "Details",
      accessor: (row) =>
        row.missingCapabilities?.join(", ") ||
        row.compatibilityReason ||
        row.matchReasons?.join(", ") ||
        "Eligible",
    },
  ];
  return (
    <PageSection
      title="Authoritative placement preview"
      description="This is the server-evaluated, project-scoped membership snapshot. Launch is bound to its digest."
      actions={
        <button
          type="button"
          className={primaryButton}
          disabled={!canLaunch || preview.selectedCount === 0}
          onClick={onLaunch}
        >
          <Rocket className="h-4 w-4" /> Launch rollout
        </button>
      }
    >
      <div className="grid gap-3 sm:grid-cols-4">
        <Metric label="Selected" value={preview.selectedCount} />
        <Metric label="Excluded / blocked" value={preview.excludedCount} />
        <Metric label="Target generation" value={preview.targetGeneration} />
        <Metric
          label="All-cluster confirmation"
          value={preview.requiresAllConfirmation ? "Required" : "No"}
        />
      </div>
      {preview.risks.length > 0 && (
        <div
          role="alert"
          className="rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm text-status-warning"
        >
          <p className="flex items-center gap-2 font-medium">
            <AlertTriangle className="h-4 w-4" /> Review before launch
          </p>
          <ul className="mt-2 list-disc space-y-1 pl-5">
            {preview.risks.map((risk) => (
              <li key={risk}>{risk.replaceAll("_", " ")}</li>
            ))}
          </ul>
        </div>
      )}
      <p className="font-mono text-xs text-muted-foreground">
        Preview digest: {preview.previewDigest}
      </p>
      <DataTable
        data={preview.decisions}
        columns={columns}
        keyExtractor={(row) => row.clusterId}
        searchable={false}
        pageSize={Math.max(preview.decisions.length, 1)}
        emptyMessage="No clusters were evaluated"
      />
      <div
        className="flex flex-col gap-3 border-t border-border pt-3 text-sm sm:flex-row sm:items-center sm:justify-between"
        aria-live="polite"
      >
        <p className="text-muted-foreground">
          {preview.decisionCount === 0
            ? "No placement decisions"
            : `Showing ${preview.decisionOffset + 1}–${preview.decisionOffset + preview.decisions.length} of ${preview.decisionCount} decisions`}
        </p>
        <div className="flex items-center gap-2">
          <button
            type="button"
            className={secondaryButton}
            disabled={!canGoBack || loadingPage}
            onClick={onPrevious}
            aria-label="Previous placement decision page"
          >
            <ChevronLeft className="h-4 w-4" /> Previous
          </button>
          <span className="min-w-16 text-center text-xs text-muted-foreground">
            Page {pageIndex + 1}
          </span>
          <button
            type="button"
            className={secondaryButton}
            disabled={
              !preview.hasMoreDecisions || !preview.nextCursor || loadingPage
            }
            onClick={onNext}
            aria-label="Next placement decision page"
          >
            Next <ChevronRight className="h-4 w-4" />
          </button>
        </div>
      </div>
    </PageSection>
  );
}

function TargetEditDialog({
  projectId,
  target,
  etag,
  onUpdated,
  onClose,
}: {
  projectId: string;
  target: DeliveryTarget;
  etag: string | number;
  onUpdated: () => void;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const defaults = placementFormDefaults(target.placement);
  const [allClusters, setAllClusters] = useState(defaults.allClusters);
  const [drift, setDrift] = useState<DriftPolicy>(
    target.reconciliationPolicy.drift,
  );
  const [formError, setFormError] = useState<Error | null>(null);
  const templates = useQuery({
    queryKey: queryKeys.delivery.configurationTemplates(projectId, {
      limit: 200,
    }),
    queryFn: ({ signal }) =>
      listDeliveryConfigurationTemplates(projectId, { limit: 200 }, signal),
  });
  const overrides = useQuery({
    queryKey: queryKeys.delivery.overrideSets(projectId, { limit: 200 }),
    queryFn: ({ signal }) =>
      listDeliveryOverrideSets(projectId, { limit: 200 }, signal),
  });
  const mutation = useMutation({
    mutationFn: (body: Parameters<typeof updateDeliveryTarget>[1]) =>
      updateDeliveryTarget(target.id, body, etag, crypto.randomUUID()),
    onSuccess: () => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.target(projectId, target.id),
      });
      client.invalidateQueries({
        queryKey: queryKeys.delivery.targetsAll(projectId),
      });
      onUpdated();
      toastSuccess("Target configuration updated");
      onClose();
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    try {
      const form = new FormData(event.currentTarget);
      const placement = placementFromForm(form, allClusters);
      if (!placementHasSelector(placement)) {
        throw new Error(
          "Select at least one explicit cluster, group, label, or expression. Empty placement selects nothing.",
        );
      }
      const maintenanceText = String(form.get("maintenance") ?? "").trim();
      const maintenanceWindowPolicy = maintenanceText
        ? (JSON.parse(maintenanceText) as Record<string, unknown>)
        : {};
      if (
        maintenanceWindowPolicy === null ||
        Array.isArray(maintenanceWindowPolicy) ||
        typeof maintenanceWindowPolicy !== "object"
      ) {
        throw new Error("Maintenance policy must be a JSON object.");
      }
      mutation.mutate({
        project_id: projectId,
        description: String(form.get("description") ?? "").trim() || undefined,
        bundle_version_id: String(form.get("bundle_version_id") ?? "").trim(),
        placement,
        rollout_policy: {
          approval_required: form.get("approval_required") === "on",
        },
        reconciliation_policy: {
          interval: String(form.get("interval")),
          retry_interval: String(form.get("retry_interval")),
          timeout: String(form.get("timeout")),
          prune: form.get("prune") === "on",
          wait: form.get("wait") === "on",
          drift,
        },
        maintenance_window_policy: maintenanceWindowPolicy,
        configuration_template_id:
          String(form.get("configuration_template_id") ?? "") || null,
        override_set_ids: form
          .getAll("override_set_ids")
          .map((value) => String(value)),
      });
    } catch (error) {
      setFormError(
        error instanceof Error
          ? error
          : new Error("Target configuration is invalid."),
      );
    }
  };
  return (
    <ModalShell
      title={`Edit ${target.name}`}
      size="xl"
      onClose={onClose}
      subtitle="Saving changes increments the target generation. Run a new authoritative preview before launching."
    >
      <form className="space-y-5" onSubmit={submit}>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Description">
            <input
              name="description"
              maxLength={4096}
              defaultValue={target.description ?? ""}
              className={inputClass}
            />
          </Field>
          <Field label="Immutable bundle version ID">
            <input
              name="bundle_version_id"
              required
              defaultValue={target.bundleVersionId}
              className={inputClass}
            />
          </Field>
        </div>
        <fieldset className="space-y-4 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Effective configuration
          </legend>
          <p className="text-sm text-muted-foreground">
            Changes create a new target generation. Resolution and conflict
            checks run before the next rollout plan is created.
          </p>
          <Field label="Base configuration template">
            <select
              name="configuration_template_id"
              defaultValue={target.configurationTemplateId ?? ""}
              className={inputClass}
            >
              <option value="">Use bundle defaults</option>
              {templates.data?.data.map((template) => (
                <option key={template.id} value={template.id}>
                  {template.name} · {template.renderer}
                </option>
              ))}
            </select>
          </Field>
          <div className="grid gap-2 sm:grid-cols-2">
            {(overrides.data?.data ?? []).map((item) => (
              <label
                key={item.id}
                className="flex items-start gap-2 rounded-md border border-border p-3 text-sm"
              >
                <input
                  name="override_set_ids"
                  type="checkbox"
                  value={item.id}
                  defaultChecked={target.overrideSetIds.includes(item.id)}
                  disabled={
                    !item.enabled && !target.overrideSetIds.includes(item.id)
                  }
                  className="mt-0.5"
                />
                <span>
                  <span className="block font-medium">{item.name}</span>
                  <span className="text-xs text-muted-foreground">
                    {item.scope} · precedence {item.precedence}
                    {!item.enabled ? " · disabled" : ""}
                  </span>
                </span>
              </label>
            ))}
            {!overrides.isLoading &&
              (overrides.data?.data ?? []).length === 0 && (
                <p className="text-sm text-muted-foreground">
                  No configuration overrides are available.
                </p>
              )}
          </div>
        </fieldset>
        <fieldset className="space-y-4 rounded-md border border-border p-4">
          <legend className="px-1 text-sm font-medium">
            Placement selector
          </legend>
          <label className="flex items-center gap-2 text-sm font-medium text-status-warning">
            <input
              type="checkbox"
              checked={allClusters}
              onChange={(event) => setAllClusters(event.target.checked)}
            />
            Select every eligible cluster in this project
          </label>
          {!allClusters && (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field label="Explicit cluster IDs (comma-separated)">
                <textarea
                  name="cluster_ids"
                  defaultValue={defaults.clusterIds}
                  className={textareaClass}
                />
              </Field>
              <Field label="Cluster group IDs (comma-separated)">
                <textarea
                  name="group_ids"
                  defaultValue={defaults.groupIds}
                  className={textareaClass}
                />
              </Field>
              <Field label="Match labels (one key=value per line)">
                <textarea
                  name="labels"
                  defaultValue={defaults.labels}
                  className={textareaClass}
                />
              </Field>
              <Field label="Expressions (one per line)">
                <textarea
                  name="expressions"
                  defaultValue={defaults.expressions}
                  className={textareaClass}
                />
              </Field>
            </div>
          )}
          <Field label="Exclude cluster IDs (comma-separated)">
            <textarea
              name="exclude_ids"
              defaultValue={defaults.excludeIds}
              className={textareaClass}
            />
          </Field>
        </fieldset>
        <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">Reconciliation</legend>
          <Field label="Interval">
            <input
              name="interval"
              required
              defaultValue={target.reconciliationPolicy.interval}
              className={inputClass}
            />
          </Field>
          <Field label="Retry interval">
            <input
              name="retry_interval"
              required
              defaultValue={target.reconciliationPolicy.retryInterval}
              className={inputClass}
            />
          </Field>
          <Field label="Timeout">
            <input
              name="timeout"
              required
              defaultValue={target.reconciliationPolicy.timeout}
              className={inputClass}
            />
          </Field>
          <Field label="Drift">
            <select
              value={drift}
              onChange={(event) => setDrift(event.target.value as DriftPolicy)}
              className={inputClass}
            >
              <option value="repair">Detect and repair</option>
              <option value="detect">Detect only</option>
              <option value="ignore">Ignore</option>
            </select>
          </Field>
          <label className="flex items-center gap-2 text-sm">
            <input
              name="prune"
              type="checkbox"
              defaultChecked={target.reconciliationPolicy.prune}
            />
            Prune removed objects
          </label>
          <label className="flex items-center gap-2 text-sm">
            <input
              name="wait"
              type="checkbox"
              defaultChecked={target.reconciliationPolicy.wait}
            />
            Wait for health
          </label>
        </fieldset>
        <div className="grid gap-4 sm:grid-cols-2">
          <Field label="Maintenance policy (JSON object)">
            <textarea
              name="maintenance"
              defaultValue={JSON.stringify(
                target.maintenanceWindowPolicy,
                null,
                2,
              )}
              className={textareaClass}
              spellCheck={false}
            />
          </Field>
          <label className="flex items-center gap-2 pt-8 text-sm">
            <input
              name="approval_required"
              type="checkbox"
              defaultChecked={target.rolloutPolicy.approvalRequired}
            />
            Require human rollout approval
          </label>
        </div>
        {(formError || mutation.isError) && (
          <ErrorMessage error={formError ?? mutation.error} />
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={mutation.isPending}
          >
            {mutation.isPending ? "Saving…" : "Save and require new preview"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function LaunchDialog({
  projectId,
  target,
  preview,
  onClose,
}: {
  projectId: string;
  target: DeliveryTarget;
  preview: PlacementPreview;
  onClose: () => void;
}) {
  const client = useQueryClient();
  const router = useRouter();
  const { entityHref } = useDeliveryWorkspace();
  const [strategyType, setStrategyType] =
    useState<RolloutStrategyType>("rolling");
  const [maxUnavailableType, setMaxUnavailableType] =
    useState<AmountType>("count");
  const [failureType, setFailureType] = useState<AmountType>("count");
  const [onFailure, setOnFailure] = useState<RolloutFailureAction>("pause");
  const [formError, setFormError] = useState<Error | null>(null);
  const mutation = useMutation({
    mutationFn: (strategy: RolloutStrategyRequest) =>
      startDeliveryRollout(
        target.id,
        {
          project_id: projectId,
          preview_digest: preview.previewDigest,
          confirm_all_clusters: preview.requiresAllConfirmation,
          strategy,
        },
        preview.targetGeneration,
        crypto.randomUUID(),
      ),
    onSuccess: (rollout) => {
      client.invalidateQueries({
        queryKey: queryKeys.delivery.rolloutsAll(projectId),
      });
      toastSuccess("Rollout launched");
      onClose();
      router.push(entityHref("rollouts", rollout.id));
    },
  });
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setFormError(null);
    try {
      const form = new FormData(event.currentTarget);
      if (preview.requiresAllConfirmation && form.get("confirm_all") !== "on")
        throw new Error("Explicit all-cluster confirmation is required.");
      const partitionsText = String(form.get("partitions") ?? "").trim();
      const partitions = partitionsText
        ? (JSON.parse(partitionsText) as RolloutStrategyRequest["partitions"])
        : undefined;
      const explicitCanaries = String(form.get("canary_ids") ?? "")
        .split(",")
        .map((id) => id.trim())
        .filter(Boolean);
      const strategy: RolloutStrategyRequest = {
        type: strategyType,
        max_concurrent: Number(form.get("max_concurrent")),
        max_unavailable: {
          type: maxUnavailableType,
          value: Number(form.get("max_unavailable")),
        },
        min_ready: String(form.get("min_ready")),
        progress_deadline: String(form.get("deadline")),
        failure_threshold: {
          type: failureType,
          value: Number(form.get("failure_threshold")),
        },
        on_failure: onFailure,
        respect_maintenance_windows: form.get("respect_windows") === "on",
        shuffle_seed:
          String(form.get("shuffle_seed") ?? "").trim() || undefined,
        ...(strategyType === "canary"
          ? {
              canary: explicitCanaries.length
                ? {
                    size: { type: "count", value: 0 },
                    cluster_ids: explicitCanaries,
                    approval_after_canary:
                      form.get("approval_after_canary") === "on",
                    soak: String(form.get("canary_soak")),
                  }
                : {
                    size: {
                      type: String(form.get("canary_size_type")) as AmountType,
                      value: Number(form.get("canary_size")),
                    },
                    approval_after_canary:
                      form.get("approval_after_canary") === "on",
                    soak: String(form.get("canary_soak")),
                  },
            }
          : {}),
        ...(strategyType === "partitioned" ? { partitions } : {}),
      };
      mutation.mutate(strategy);
    } catch (error) {
      setFormError(
        error instanceof Error
          ? error
          : new Error("Rollout strategy is invalid."),
      );
    }
  };
  return (
    <ModalShell
      title="Launch rollout"
      size="xl"
      onClose={onClose}
      subtitle="This action freezes placement, immutable bundle revision, strategy, budgets, and the previous known-good version."
    >
      <form className="space-y-5" onSubmit={submit}>
        <div className="grid gap-3 rounded-md border border-border bg-muted/20 p-4 sm:grid-cols-3">
          <Detail label="Bundle version" value={preview.bundleVersionId} mono />
          <Detail label="Selected clusters" value={preview.selectedCount} />
          <Detail label="Preview digest" value={preview.previewDigest} mono />
        </div>
        <div className="grid gap-4 sm:grid-cols-3">
          <Field label="Strategy">
            <select
              value={strategyType}
              onChange={(e) =>
                setStrategyType(e.target.value as RolloutStrategyType)
              }
              className={inputClass}
            >
              <option value="all_at_once">All at once</option>
              <option value="rolling">Rolling</option>
              <option value="canary">Canary</option>
              <option value="partitioned">Partitioned cohorts</option>
            </select>
          </Field>
          <Field label="Maximum concurrent">
            <input
              name="max_concurrent"
              required
              type="number"
              min={1}
              defaultValue={10}
              className={inputClass}
            />
          </Field>
          <Field label="Stable shuffle seed (optional)">
            <input name="shuffle_seed" maxLength={128} className={inputClass} />
          </Field>
        </div>
        <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
          <legend className="px-1 text-sm font-medium">Safety budgets</legend>
          <Field label="Maximum unavailable">
            <div className="flex gap-2">
              <select
                value={maxUnavailableType}
                onChange={(e) =>
                  setMaxUnavailableType(e.target.value as AmountType)
                }
                className={inputClass}
              >
                <option value="count">Count</option>
                <option value="percent">Percent</option>
              </select>
              <input
                name="max_unavailable"
                required
                type="number"
                min={0}
                defaultValue={1}
                className={inputClass}
              />
            </div>
          </Field>
          <Field label="Failure threshold">
            <div className="flex gap-2">
              <select
                value={failureType}
                onChange={(e) => setFailureType(e.target.value as AmountType)}
                className={inputClass}
              >
                <option value="count">Count</option>
                <option value="percent">Percent</option>
              </select>
              <input
                name="failure_threshold"
                required
                type="number"
                min={1}
                defaultValue={1}
                className={inputClass}
              />
            </div>
          </Field>
          <Field label="On failure">
            <select
              value={onFailure}
              onChange={(e) =>
                setOnFailure(e.target.value as RolloutFailureAction)
              }
              className={inputClass}
            >
              <option value="pause">Pause</option>
              <option value="abort">Abort</option>
              <option value="rollback">Roll back known-good version</option>
            </select>
          </Field>
          <Field label="Minimum ready">
            <input
              name="min_ready"
              required
              defaultValue="30s"
              className={inputClass}
            />
          </Field>
          <Field label="Progress deadline">
            <input
              name="deadline"
              required
              defaultValue="30m"
              className={inputClass}
            />
          </Field>
          <label className="flex items-center gap-2 pt-7 text-sm">
            <input name="respect_windows" type="checkbox" defaultChecked />{" "}
            Respect maintenance windows
          </label>
        </fieldset>
        {strategyType === "canary" && (
          <fieldset className="grid gap-4 rounded-md border border-border p-4 sm:grid-cols-3">
            <legend className="px-1 text-sm font-medium">Canary cohort</legend>
            <Field label="Explicit canary cluster IDs (optional)">
              <input name="canary_ids" className={inputClass} />
            </Field>
            <Field label="Canary size if not explicit">
              <div className="flex gap-2">
                <select name="canary_size_type" className={inputClass}>
                  <option value="count">Count</option>
                  <option value="percent">Percent</option>
                </select>
                <input
                  name="canary_size"
                  type="number"
                  min={1}
                  defaultValue={1}
                  className={inputClass}
                />
              </div>
            </Field>
            <Field label="Soak">
              <input
                name="canary_soak"
                required
                defaultValue="5m"
                className={inputClass}
              />
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <input
                name="approval_after_canary"
                type="checkbox"
                defaultChecked
              />{" "}
              Require approval after canary
            </label>
          </fieldset>
        )}
        {strategyType === "partitioned" && (
          <Field label="Ordered partition definitions (JSON array)">
            <textarea
              name="partitions"
              required
              className={textareaClass}
              placeholder='[{"name":"staging","selector":{"all_clusters":false,"match_labels":{"environment":"staging"}},"approval_required":true,"soak":"10m"}]'
            />
          </Field>
        )}
        {target.rolloutPolicy.approvalRequired && (
          <p className="rounded-md border border-status-warning/30 bg-status-warning/10 p-3 text-sm text-status-warning">
            This target requires human approval. The rollout will stop at an
            approval gate bound to its exact digest and expiry.
          </p>
        )}
        {preview.requiresAllConfirmation && (
          <label className="flex items-start gap-2 rounded-md border border-status-error/30 bg-status-error/10 p-3 text-sm">
            <input name="confirm_all" type="checkbox" className="mt-1" />
            <span>
              <strong>Confirm all eligible project clusters.</strong> This broad
              placement is intentionally protected by enhanced confirmation.
            </span>
          </label>
        )}
        {(formError || mutation.isError) && (
          <ErrorMessage error={formError ?? mutation.error} />
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className={secondaryButton} onClick={onClose}>
            Cancel
          </button>
          <button
            type="submit"
            className={primaryButton}
            disabled={mutation.isPending}
          >
            {mutation.isPending ? "Launching…" : "Launch frozen rollout"}
          </button>
        </div>
      </form>
    </ModalShell>
  );
}

function Metric({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="rounded-lg border border-border bg-card p-4">
      <p className="text-xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p className="mt-1 text-xl font-semibold">{value}</p>
    </div>
  );
}
function Field({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1.5 text-sm">
      <span className="font-medium">{label}</span>
      {children}
    </label>
  );
}
function DeliveryTargetDetailRedirect() {
  const { targetId } = useParams<{ targetId: string }>();
  return (
    <RedirectDeliveryDetail tab="targets" id={targetId}>
      <TargetDetailPage />
    </RedirectDeliveryDetail>
  );
}

export const Route = createFileRoute("/dashboard/delivery/targets/$targetId/")({
  component: DeliveryTargetDetailRedirect,
});
