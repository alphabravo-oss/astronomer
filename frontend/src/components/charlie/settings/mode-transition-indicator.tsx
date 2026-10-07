import { CheckCircle2, Loader2 } from "lucide-react";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn } from "@/lib/utils";
import type { CharlieMode } from "@/lib/api/charlie-admin";
import { productModeLabel, type ModeTransitionState } from "./mode-tab-model";

export function ModeTransitionIndicator({
  transition,
  workReady,
  disabledConfirmed,
  confirmationPending,
  settling,
  authoritative,
  requested,
  agent,
}: {
  transition: ModeTransitionState;
  workReady: boolean;
  disabledConfirmed: boolean;
  confirmationPending: boolean;
  settling: boolean;
  authoritative: CharlieMode;
  requested: CharlieMode;
  agent?: { readyReplicas: number; desiredReplicas: number } | null;
}) {
  return (
    <div
      role="status"
      aria-live="polite"
      data-testid="charlie-mode-transition"
      data-phase={transition.phase}
      className={cn(
        "rounded-lg border p-4",
        transition.phase === "ready" &&
          "border-status-success/40 bg-status-success/5",
        (transition.phase === "applying" || transition.phase === "verifying") &&
          "border-status-info/40 bg-status-info/5",
        transition.phase === "failed" &&
          "border-status-error/40 bg-status-error/5",
        transition.phase === "idle" && workReady && "border-border bg-muted/20",
        transition.phase === "idle" &&
          !workReady &&
          "border-status-warning/40 bg-status-warning/5",
      )}
    >
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 space-y-1">
          <p className="text-sm font-medium">
            {transition.phase === "applying" && "Changing mode"}
            {transition.phase === "verifying" && "Validating product agents"}
            {transition.phase === "ready" && "Mode ready for work"}
            {transition.phase === "failed" && "Mode change incomplete"}
            {transition.phase === "idle" &&
              (disabledConfirmed
                ? "Charlie is confirmed disabled"
                : workReady
                  ? "Current mode is ready for work"
                  : confirmationPending
                    ? "Mode change is not yet confirmed"
                    : "Agent ceiling not fully verified")}
          </p>
          <p className="text-xs text-muted-foreground">
            {transition.message ||
              (disabledConfirmed
                ? "Fail-closed is verified on both product-agent replicas. Raise mode after any pending catalog review."
                : workReady
                  ? `${productModeLabel[authoritative]} is authoritative and both product-agent replicas match the ceiling.`
                  : confirmationPending
                    ? `Both product-agent replicas already report ${productModeLabel[requested]}. Charlie has not confirmed that as the live authority yet.`
                    : "Charlie stays fail-closed for elevated work until both replicas report the requested ceiling.")}
          </p>
        </div>
        <StatusBadge
          status={
            transition.phase === "idle" && disabledConfirmed
              ? "disabled"
              : transition.phase === "ready" ||
                  (transition.phase === "idle" && workReady)
                ? "healthy"
                : transition.phase === "failed"
                  ? "unavailable"
                  : "degraded"
          }
          label={
            transition.phase === "applying"
              ? "Changing"
              : transition.phase === "verifying"
                ? "Validating"
                : transition.phase === "idle" && disabledConfirmed
                  ? "Disabled"
                  : transition.phase === "ready" ||
                      (transition.phase === "idle" && workReady)
                    ? "Ready"
                    : transition.phase === "failed"
                      ? "Failed"
                      : "Not ready"
          }
          pulse={settling}
          icon={
            settling ? (
              <Loader2 className="h-3 w-3 animate-spin motion-reduce:animate-none" />
            ) : transition.phase === "ready" ||
              (transition.phase === "idle" && workReady) ? (
              <CheckCircle2 className="h-3 w-3" />
            ) : undefined
          }
        />
      </div>
      {(settling || transition.phase === "ready") && transition.target && (
        <ol className="mt-3 grid gap-2 text-xs sm:grid-cols-3">
          {(
            [
              ["Confirm", true],
              [
                "Apply ceiling",
                transition.phase === "verifying" ||
                  transition.phase === "ready",
              ],
              ["Agents ready", transition.phase === "ready"],
            ] as const
          ).map(([label, done], index) => (
            <li
              key={label}
              className={cn(
                "rounded-md border px-3 py-2",
                done
                  ? "border-status-success/30 bg-status-success/5 text-foreground"
                  : "border-border text-muted-foreground",
              )}
            >
              <span className="font-medium">
                {index + 1}. {label}
              </span>
              {index === 1 && transition.phase === "applying" && (
                <span className="mt-0.5 block text-muted-foreground">
                  Rolling CHARLIE_MODE on both agent replicas
                </span>
              )}
              {index === 2 && transition.phase === "verifying" && (
                <span className="mt-0.5 block text-muted-foreground">
                  Waiting for workload_ceiling_ready and ready replicas
                  {agent
                    ? ` (${agent.readyReplicas}/${agent.desiredReplicas})`
                    : ""}
                </span>
              )}
            </li>
          ))}
        </ol>
      )}
    </div>
  );
}
