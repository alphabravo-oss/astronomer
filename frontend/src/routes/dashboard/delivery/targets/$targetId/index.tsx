import { createFileRoute, useParams } from "@tanstack/react-router";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, Eye, Pause, Pencil, Play, Trash2 } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { PageHeader, PageSection, PageShell } from "@/components/ui/page";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { ActionButton } from "@/components/ui/action-button";
import {
  DeliveryPhaseBadge,
  DeliveryProjectGate,
  DeliveryShell,
  Detail,
  DetailGrid,
  ErrorMessage,
  RedirectDeliveryDetail,
  useDeliveryWorkspace,
  withProjectQuery,
} from "@/components/delivery/shared";
import {
  deleteDeliveryTarget,
  getDeliveryTarget,
  orphanDeliveryTarget,
  previewDeliveryTarget,
  updateDeliveryTarget,
  type PlacementPreview,
} from "@/lib/api/delivery-targets";
import { queryKeys } from "@/lib/query-keys";
import { useCurrentUser } from "@/lib/hooks/auth";
import { can, isSuperuser } from "@/lib/permissions";
import { useNavigate } from "@tanstack/react-router";
import { toastSuccess } from "@/lib/toast";
import { PreviewPanel } from "./-preview-panel";
import { TargetEditDialog } from "./-target-edit-dialog";
import { LaunchDialog } from "./-launch-dialog";

export function TargetDetailPage() {
  const { targetId } = useParams({ strict: false }) as { targetId: string };
  const { projectId, projects, projectQuery, setProjectId, listHref } =
    useDeliveryWorkspace();
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
  const navigate = useNavigate();
  const query = useQuery({
    queryKey: queryKeys.delivery.target(projectId, targetId),
    queryFn: ({ signal }) => getDeliveryTarget(projectId, targetId, signal),
    enabled: Boolean(projectId && targetId && allowed),
  });
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
      void navigate({ to: withProjectQuery(listHref("targets"), projectId) });
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
      void navigate({ to: withProjectQuery(listHref("targets"), projectId) });
    },
  });
  const target = query.data?.data;
  if (allowed && projectId && (query.isLoading || query.isError || !target)) {
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
            <QueryStates
              query={query}
              permission="delivery_targets:read"
              notFound={
                <EmptyState
                  icon={AlertTriangle} title="Target not found"
                  description="This delivery target no longer exists or is outside the selected project."
                  actionLabel="Back to targets" actionHref="/dashboard/delivery/targets"
                />
              }
            >
              {() => null}
            </QueryStates>
          </PageShell>
        </DeliveryProjectGate>
      </DeliveryShell>
    );
  }
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
          <RouterLink
            to={withProjectQuery(listHref("targets"), projectId)}
            className="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
          >
            <ArrowLeft className="h-4 w-4" /> Targets
          </RouterLink>
          <PageHeader
            eyebrow="Delivery target"
            title={target?.name ?? "Target"}
            description={target?.description || "Placement and rollout policy"}
            actions={
              target ? (
                <>
                  <ActionButton
                    disabled={!canUpdate}
                    onClick={() => setEditing(true)}
                    icon={<Pencil className="h-4 w-4" />}
                  >
                    Edit configuration
                  </ActionButton>
                  <ActionButton
                    disabled={!canUpdate || suspendMutation.isPending}
                    onClick={() => suspendMutation.mutate()}
                    icon={
                      target.suspended ? (
                        <Play className="h-4 w-4" />
                      ) : (
                        <Pause className="h-4 w-4" />
                      )
                    }
                  >
                    {target.suspended ? "Resume target" : "Suspend target"}
                  </ActionButton>
                  <ActionButton
                    disabled={previewMutation.isPending}
                    loading={previewMutation.isPending}
                    loadingLabel="Evaluating…"
                    onClick={() =>
                      previewMutation.mutate({
                        cursor: "",
                        pageIndex: 0,
                        reset: true,
                      })
                    }
                    intent="primary"
                    icon={<Eye className="h-4 w-4" />}
                  >
                    Preview placement
                  </ActionButton>
                  {canDelete && (
                    <ActionButton
                      onClick={() => setDeleting(true)}
                      intent="destructive"
                      icon={<Trash2 className="h-4 w-4" />}
                    >
                      Delete
                    </ActionButton>
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
              </DetailGrid>
              <PageSection title="Placement intent">
                <pre className="max-h-80 overflow-auto rounded-lg border border-border bg-muted/30 p-4 text-xs">
                  {JSON.stringify(target.placement, null, 2)}
                </pre>
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
          <ActionButton
            onClick={() => {
              setDeleting(false);
              setOrphaning(true);
            }}
          >
            Orphan workloads instead
          </ActionButton>
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

function DeliveryTargetDetailRedirect() {
  const { targetId } = useParams({ strict: false }) as { targetId: string };
  return (
    <RedirectDeliveryDetail tab="targets" id={targetId}>
      <TargetDetailPage />
    </RedirectDeliveryDetail>
  );
}

export const Route = createFileRoute("/dashboard/delivery/targets/$targetId/")({
  component: DeliveryTargetDetailRedirect,
});
