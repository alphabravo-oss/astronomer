// Sprint 23 - shared registration-timeline component.
//
// Used by both the unified registration flow and the cluster-detail Adoption
// tab. Registration progress stays inline so operators retain the install
// command context while the cluster connects.
//
// Subscribes to the wizard SSE stream + polls /clusters/{id}/registration/
// status/ as a fallback. Renders each step row with status icon, label,
// detail, optional progress bar, and a Retry button on failed rows.

import { useEffect, useEffectEvent, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/query-keys";
import { apiErrorStatus } from "@/lib/api/errors";
import { toastError, toastSuccess } from "@/lib/toast";
import {
  getRegistrationStatus,
  retryRegistrationStep,
} from "@/lib/api/cluster-registration";
import type {
  RegistrationStatusView,
  RegistrationStepView,
} from "@/lib/api/cluster-registration";
import { useLiveEvents } from "@/lib/live/hooks";
import { useLiveStatus } from "@/lib/live/status-store";
import { ActionButton } from "@/components/ui/action-button";
import {
  OperationTimeline,
  type OperationTimelineStepStatus,
} from "@/components/ui/operation-timeline";

interface Props {
  clusterId: string;
  /** Pass `true` when embedded inside the cluster-detail tab so the
   *  component renders without the wizard's "Step 3 of 3" header chrome.
   */
  embedded?: boolean;
  /** Optional callback invoked when the registration completes (status
   *  transitions to `ready`). Lets the host page swap chrome or
   *  redirect.
   */
  onReady?: () => void;
  /** Limit the embedded view without creating a second adoption navigator. */
  view?: "all" | "readiness" | "plan";
}

export function RegistrationTimeline({
  clusterId,
  embedded = false,
  onReady,
  view = "all",
}: Props) {
  const queryClient = useQueryClient();
  const streamStatus = useLiveStatus();
  const statusKey = queryKeys.clusterPages.registrationStatus(clusterId);
  const statusQuery = useQuery({
    queryKey: statusKey,
    queryFn: ({ signal }) => getRegistrationStatus(clusterId, { signal }),
    enabled: !!clusterId,
    retry: false,
    refetchInterval: (query) => {
      const phase = query.state.data?.phase;
      return streamStatus === "open" ||
        phase === "ready" ||
        phase === "failed" ||
        apiErrorStatus(query.state.error) === 404
        ? false
        : 5000;
    },
  });
  const status = statusQuery.data;
  const notFound = apiErrorStatus(statusQuery.error) === 404;
  const { refetch: refresh } = statusQuery;
  const [retrying, setRetrying] = useState<string | null>(null);

  const live = useLiveEvents();
  useEffect(() => {
    // Live envelopes are camelized centrally (lib/live/envelope.ts).
    const off1 = live.subscribe("cluster.registration.step", (payload) => {
      const data = (payload as { data?: { clusterId?: string } }).data;
      if (data?.clusterId === clusterId) refresh();
    });
    const off2 = live.subscribe("cluster.registration.phase", (payload) => {
      const data = (payload as { data?: { clusterId?: string } }).data;
      if (data?.clusterId === clusterId) refresh();
    });
    return () => {
      off1();
      off2();
    };
  }, [live, clusterId, refresh]);

  // Fire onReady once when we transition into ready phase.
  const notifyReady = useEffectEvent(() => onReady?.());
  useEffect(() => {
    if (status?.phase === "ready") notifyReady();
  }, [status?.phase]);

  const onRetry = async (step: RegistrationStepView) => {
    setRetrying(step.id);
    try {
      const s = await retryRegistrationStep(clusterId, step.id);
      queryClient.setQueryData(statusKey, s);
      toastSuccess("Retry queued");
    } catch (e) {
      const msg = e instanceof Error ? e.message : "Unknown error";
      toastError(`Retry failed: ${msg}`);
    } finally {
      setRetrying(null);
    }
  };

  if (notFound) {
    return (
      <div className="text-sm text-muted-foreground py-4">
        No registration record for this cluster - it likely predates the wizard
        or has already been cleaned up.
      </div>
    );
  }

  const visibleSteps = (status?.steps ?? []).filter((step) => {
    const readiness = [
      "cluster_created",
      "manifest_generated",
      "agent_connected",
    ].includes(step.stepName);
    if (view === "readiness") return readiness;
    if (view === "plan") return !readiness;
    return true;
  });
  const steps = visibleSteps.map((step) => ({
    id: step.id,
    label: step.label,
    status: timelineStatus(step.status),
    detail:
      step.detail && Object.keys(step.detail).length > 0
        ? Object.entries(step.detail)
            .map(([k, v]) => `${k}: ${String(v)}`)
            .join(" • ")
        : undefined,
    error: step.errorMessage,
    progressPct: step.progressPct,
    action:
      step.status === "failed" ? (
        <ActionButton
          intent="ghost"
          size="sm"
          onClick={() => onRetry(step)}
          loading={retrying === step.id}
          loadingLabel="Retrying..."
        >
          Retry
        </ActionButton>
      ) : undefined,
  }));

  return (
    <OperationTimeline
      header={<PhaseBadge phase={status?.phase} />}
      headerMeta={
        status?.startedAt
          ? `Started ${new Date(status.startedAt).toLocaleString()}`
          : undefined
      }
      steps={steps}
      emptyLabel={status ? "Waiting for first step..." : "Loading..."}
      footer={
        !embedded && status?.phase === "failed" ? (
          <div className="px-4 py-3 border-t border-border bg-status-error/5">
            <p className="text-xs text-muted-foreground">
              Use the Retry buttons above to re-run a failing step, or talk to
              your platform team if the issue persists.
            </p>
          </div>
        ) : undefined
      }
    />
  );
}

function timelineStatus(
  status: RegistrationStepView["status"],
): OperationTimelineStepStatus {
  switch (status) {
    case "success":
      return "success";
    case "running":
      return "running";
    case "failed":
      return "failed";
    case "skipped":
      return "skipped";
    default:
      return "pending";
  }
}

export function PhaseBadge({
  phase,
}: {
  phase: RegistrationStatusView["phase"] | undefined;
}) {
  if (!phase)
    return <span className="text-xs text-muted-foreground">Loading...</span>;
  const colour =
    phase === "ready"
      ? "text-status-success"
      : phase === "failed"
        ? "text-status-error"
        : phase === "provisioning"
          ? "text-primary"
          : "text-muted-foreground";
  const label =
    phase === "awaiting_agent"
      ? "awaiting agent"
      : phase === "provisioning"
        ? "applying baseline"
        : phase;
  return (
    <span className={`text-xs font-medium uppercase tracking-wide ${colour}`}>
      Phase: {label}
    </span>
  );
}
