import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
} from "lucide-react";
import { StatePanel } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { queryKeys } from "@/lib/query-keys";
import { toastApiError, toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";
import {
  acknowledgeCharlieDisclosure,
  emergencyDisableCharlie,
  getCharlieAgent,
  getCharlieMode,
  updateCharlieMode,
  type CharlieMode,
} from "@/lib/api/charlie-admin";
import { Meta, Section, Unavailable, button } from "./shared";

const modeHelp: Record<CharlieMode, string> = {
  disabled:
    "No new sessions, triggers, findings, claims, approvals, actions, or MCP calls. Health and audit remain available.",
  read_only:
    "Charlie can investigate and explain through authorized reads, but cannot propose executable approvals.",
  approval:
    "Includes Read only. Charlie may propose bounded actions; an eligible authorized user must approve each exact action.",
  auto: "Includes Read only and Approval required. Charlie may additionally execute only capabilities explicitly allowed by current product policy and disclosure.",
};
const productModeLabel: Record<CharlieMode, string> = {
  disabled: "Disabled",
  read_only: "Read only",
  approval: "Approval required",
  auto: "Automation",
};
/** What operators should expect after a successful transition into each mode. */
const modeAllowedSummary: Record<CharlieMode, string[]> = {
  disabled: [
    "No new Charlie sessions, triggers, findings, or MCP tool calls",
    "Health, configuration, and audit remain available",
  ],
  read_only: [
    "Chat, investigation, and authorized product reads",
    "No product writes — write requests stay guidance-only",
  ],
  approval: [
    "All read-only investigation capabilities",
    "Bounded writes only after an exact human approval of the proposed action",
  ],
  auto: [
    "All read-only investigation capabilities",
    "Human-approved writes still available",
    "Only centrally allowlisted, auto-eligible writes may run without a click",
    "Live RBAC, disclosure, and policy are rechecked on every write",
  ],
};

type ModeTransitionPhase =
  | "idle"
  | "applying"
  | "verifying"
  | "ready"
  | "failed";

type ModeTransitionState = {
  phase: ModeTransitionPhase;
  target?: CharlieMode;
  from?: CharlieMode;
  message?: string;
  startedAt?: number;
};

/** Agent ceiling verified and live mode matches — safe for product work. */
export function charlieModeWorkReady(
  mode: {
    requested: CharlieMode;
    authoritative: CharlieMode;
    workloadCeilingReady: boolean;
    disablePending?: boolean;
    emergencyDisabled: boolean;
  },
  agent?: {
    desiredReplicas: number;
    readyReplicas: number;
    replicas?: Array<{ state: string }>;
  } | null,
): boolean {
  if (mode.requested !== mode.authoritative) return false;
  if (!mode.workloadCeilingReady) return false;
  if (mode.disablePending) return false;
  if (mode.emergencyDisabled && mode.authoritative !== "disabled") return false;
  if (agent) {
    if (
      agent.desiredReplicas > 0 &&
      agent.readyReplicas < agent.desiredReplicas
    ) {
      return false;
    }
    if (
      agent.replicas?.some(
        (replica) =>
          replica.state === "degraded" || replica.state === "unavailable",
      )
    ) {
      return false;
    }
  }
  return true;
}

export function ModeTab() {
  const qc = useQueryClient();
  const [next, setNext] = useState<CharlieMode>();
  const [emergency, setEmergency] = useState(false);
  const [transition, setTransition] = useState<ModeTransitionState>({
    phase: "idle",
  });
  const settling =
    transition.phase === "applying" || transition.phase === "verifying";

  const q = useQuery({
    queryKey: queryKeys.charlie.adminMode,
    queryFn: getCharlieMode,
    retry: false,
    refetchInterval: (query) => {
      if (settling) return 2000;
      const data = query.state.data;
      if (data && !charlieModeWorkReady(data)) return 3000;
      return false;
    },
  });
  const agentQ = useQuery({
    queryKey: queryKeys.charlie.adminAgent,
    queryFn: getCharlieAgent,
    retry: false,
    enabled: settling || (!!q.data && !q.data.workloadCeilingReady),
    refetchInterval: settling ? 2000 : false,
  });

  // After apply, poll until both mode ceiling and agent replicas are ready.
  useEffect(() => {
    if (transition.phase !== "verifying" || !transition.target) return;
    const mode = q.data;
    if (!mode) return;
    const target = transition.target;
    if (mode.authoritative !== target || mode.requested !== target) return;
    if (!charlieModeWorkReady(mode, agentQ.data)) return;
    setTransition((prev) => ({
      ...prev,
      phase: "ready",
      message: `${productModeLabel[target]} is live. Both product-agent replicas report the verified ceiling.`,
    }));
    toastSuccess(
      `Charlie is ready in ${productModeLabel[target]} mode`,
    );
    void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
    void qc.invalidateQueries({ queryKey: queryKeys.charlie.overview });
  }, [
    transition.phase,
    transition.target,
    q.data,
    agentQ.data,
    qc,
  ]);

  // Safety timeout so "verifying" never hangs forever in the UI.
  useEffect(() => {
    if (transition.phase !== "verifying" || !transition.startedAt) return;
    const timer = window.setTimeout(() => {
      setTransition((prev) => {
        if (prev.phase !== "verifying") return prev;
        return {
          ...prev,
          phase: "failed",
          message:
            "Mode was requested, but agent ceiling verification did not complete in time. Refresh or check the Agent tab.",
        };
      });
    }, 180_000);
    return () => window.clearTimeout(timer);
  }, [transition.phase, transition.startedAt]);

  const change = useMutation({
    mutationFn: (mode: CharlieMode) =>
      updateCharlieMode(mode, q.data!.revision),
    onMutate: (mode) => {
      setNext(undefined);
      setTransition({
        phase: "applying",
        target: mode,
        from: q.data?.authoritative,
        message: `Applying ${productModeLabel[mode]} and rolling product-agent ceiling…`,
        startedAt: Date.now(),
      });
    },
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminMode, v);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      const readyNow = charlieModeWorkReady(v, agentQ.data);
      if (readyNow) {
        setTransition((prev) => ({
          phase: "ready",
          target: v.authoritative,
          from: prev.from,
          message: `${productModeLabel[v.authoritative]} is live and product agents report the verified ceiling.`,
          startedAt: prev.startedAt,
        }));
        toastSuccess(
          `Charlie is ready in ${productModeLabel[v.authoritative]} mode`,
        );
        return;
      }
      setTransition((prev) => ({
        ...prev,
        phase: "verifying",
        target: v.authoritative,
        message: `Authoritative mode is ${productModeLabel[v.authoritative]}. Verifying both product-agent replicas…`,
      }));
    },
    onError: (e) => {
      setTransition((prev) => ({
        ...prev,
        phase: "failed",
        message: "Mode change was not confirmed by Charlie.",
      }));
      toastApiError("Mode change was not confirmed", e);
    },
  });
  const disable = useMutation({
    mutationFn: () => emergencyDisableCharlie(q.data!.revision),
    onMutate: () => {
      setEmergency(false);
      setTransition({
        phase: "applying",
        target: "disabled",
        from: q.data?.authoritative,
        message: "Applying emergency disable…",
        startedAt: Date.now(),
      });
    },
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminMode, v);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminAgent });
      if (charlieModeWorkReady(v, agentQ.data)) {
        setTransition({
          phase: "ready",
          target: "disabled",
          message: "Emergency disable is confirmed on the product plane.",
        });
      } else {
        setTransition({
          phase: "verifying",
          target: "disabled",
          message: "Emergency disable recorded. Confirming agent propagation…",
          startedAt: Date.now(),
        });
      }
      toastSuccess("Emergency disable applied locally");
    },
    onError: (e) => {
      setTransition((prev) => ({
        ...prev,
        phase: "failed",
        message: "Emergency disable failed.",
      }));
      toastApiError("Emergency disable failed", e);
    },
  });
  const acknowledge = useMutation({
    mutationFn: (digest: string) => acknowledgeCharlieDisclosure(digest),
    onSuccess: (v) => {
      qc.setQueryData(queryKeys.charlie.adminMode, v);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminConnection });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.adminKubernetesVisibility });
      toastSuccess("Disclosure digest acknowledged");
    },
    onError: (e) => toastApiError("Disclosure acknowledgement failed", e),
  });
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading Charlie mode"
      />
    );
  if (q.isError || !q.data)
    return <Unavailable name="Mode control" retry={() => void q.refetch()} />;
  const m = q.data;
  const needsAck =
    !!m.disclosureDigest &&
    m.disclosureDigest !== m.acknowledgedDisclosureDigest;
  const workReady = charlieModeWorkReady(m, agentQ.data);
  const modeBusy = change.isPending || disable.isPending || settling;
  return (
    <div className="space-y-4">
      <Section
        title="Authority mode"
        description="Charlie is authoritative after readback. Drift can only reduce authority; the UI never assumes a requested mode took effect."
      >
        {/* End-to-end transition indicator: confirm → apply → verify → ready */}
        <div
          role="status"
          aria-live="polite"
          data-testid="charlie-mode-transition"
          data-phase={transition.phase}
          className={cn(
            "rounded-lg border p-4",
            transition.phase === "ready" &&
              "border-status-success/40 bg-status-success/5",
            (transition.phase === "applying" ||
              transition.phase === "verifying") &&
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
                {transition.phase === "verifying" &&
                  "Validating product agents"}
                {transition.phase === "ready" && "Mode ready for work"}
                {transition.phase === "failed" && "Mode change incomplete"}
                {transition.phase === "idle" &&
                  (workReady
                    ? "Current mode is ready for work"
                    : "Agent ceiling not fully verified")}
              </p>
              <p className="text-xs text-muted-foreground">
                {transition.message ||
                  (workReady
                    ? `${productModeLabel[m.authoritative]} is authoritative and both product-agent replicas match the ceiling.`
                    : "Charlie stays fail-closed for elevated work until both replicas report the requested ceiling.")}
              </p>
            </div>
            <StatusBadge
              status={
                transition.phase === "ready" ||
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
                      {agentQ.data
                        ? ` (${agentQ.data.readyReplicas}/${agentQ.data.desiredReplicas})`
                        : ""}
                    </span>
                  )}
                </li>
              ))}
            </ol>
          )}
        </div>

        <div
          className="grid gap-3 md:grid-cols-2"
          role="group"
          aria-label="Charlie authority mode"
        >
          {(["disabled", "read_only", "approval", "auto"] as CharlieMode[]).map(
            (mode) => (
              <button
                key={mode}
                type="button"
                aria-pressed={m.authoritative === mode}
                disabled={
                  m.authoritative === mode ||
                  (needsAck && mode !== "disabled") ||
                  (mode === "auto" && m.autoReadiness?.ready === false) ||
                  modeBusy
                }
                onClick={() => setNext(mode)}
                className={cn(
                  "rounded-lg border p-4 text-left",
                  m.authoritative === mode
                    ? "border-primary bg-primary/5"
                    : "border-border hover:bg-accent",
                  settling &&
                    transition.target === mode &&
                    "ring-2 ring-status-info/40",
                )}
              >
                <span className="font-medium">{productModeLabel[mode]}</span>
                <span className="mt-1 block text-xs text-muted-foreground">
                  {modeHelp[mode]}
                </span>
              </button>
            ),
          )}
        </div>
        {needsAck && (
          <p role="status" className="text-sm text-status-warning">
            Review and acknowledge the current disclosure before enabling a
            non-disabled authority mode.
          </p>
        )}
        <div className="rounded-lg border p-4">
          <div className="flex items-center justify-between gap-3">
            <div>
              <p className="text-sm font-medium">Automatic-action readiness</p>
              <p className="mt-1 text-xs text-muted-foreground">
                Charlie can enter automation only when every product-side safety
                prerequisite is currently satisfied.
              </p>
            </div>
            <StatusBadge
              status={m.autoReadiness?.ready ? "healthy" : "degraded"}
              label={m.autoReadiness?.ready ? "Ready" : "Blocked"}
            />
          </div>
          {m.autoReadiness?.blockers?.length ? (
            <ul className="mt-3 space-y-2">
              {m.autoReadiness.blockers.map((blocker) => (
                <li key={blocker.code} className="rounded-md bg-muted p-3 text-xs">
                  <p className="font-medium">{blocker.message}</p>
                  <p className="mt-1 text-muted-foreground">
                    Next action: {blocker.nextAction}
                  </p>
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-3 text-xs text-muted-foreground">
              {m.autoReadiness
                ? "No product-side readiness blockers are active."
                : "This Charlie version did not publish structured auto-readiness data."}
            </p>
          )}
        </div>
        <dl className="grid grid-cols-2 gap-4">
          <Meta label="Requested" value={productModeLabel[m.requested]} />
          <Meta
            label="Authoritative"
            value={productModeLabel[m.authoritative]}
          />
          <Meta label="Revision" value={m.revision} />
          <Meta
            label="Product-agent ceiling"
            value={`${productModeLabel[m.workloadCeiling]}${m.workloadCeilingReady ? " · both replicas verified" : " · rollout not verified"}`}
          />
          <Meta
            label="Emergency disabled"
            value={m.emergencyDisabled ? "Yes" : "No"}
          />
          <Meta
            label="Disable propagation"
            value={m.disablePending ? "Pending agent confirmation" : "Confirmed"}
          />
        </dl>
        {!m.workloadCeilingReady && (
          <p role="status" className="text-sm text-status-warning">
            Charlie authority remains fail-closed until the non-pruning Argo
            rollout is healthy and both product-agent replicas report the exact
            requested ceiling.
          </p>
        )}
        <div aria-live="polite" className="rounded-lg border p-4">
          <p className="text-sm font-medium">Authoritative effects</p>
          {m.effects.length ? (
            <ul className="mt-2 list-disc space-y-1 pl-5 text-xs text-muted-foreground">
              {m.effects.map((effect) => (
                <li key={effect}>{effect}</li>
              ))}
            </ul>
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">
              Charlie reported no additional effects.
            </p>
          )}
        </div>
        {needsAck && (
          <div className="rounded-lg border border-status-warning/40 bg-status-warning/5 p-4">
            <p className="text-sm font-medium">Disclosure changed</p>
            <p className="mt-1 break-all text-xs text-muted-foreground">
              Digest: {m.disclosureDigest}
            </p>
            <button
              onClick={() => acknowledge.mutate(m.disclosureDigest!)}
              className={`${button} mt-3`}
            >
              Acknowledge reviewed disclosure
            </button>
          </div>
        )}
      </Section>
      <Section
        title="Emergency control"
        description="Immediately fail closed for Charlie activity while preserving health, configuration, and audit access."
      >
        <button
          disabled={
            m.emergencyDisabled || m.disablePending || disable.isPending || settling
          }
          onClick={() => setEmergency(true)}
          className={`${button} border-status-error text-status-error`}
        >
          <AlertTriangle className="h-4 w-4" />
          Emergency Disable
        </button>
      </Section>
      <ConfirmDialog
        open={!!next}
        onClose={() => setNext(undefined)}
        onConfirm={() => next && change.mutate(next)}
        title={
          next
            ? `Change to ${productModeLabel[next]}`
            : "Change Charlie mode"
        }
        description={
          next
            ? `${modeHelp[next]} After you confirm, Astronomer rolls the product-agent ceiling, then validates both replicas before reporting ready.`
            : ""
        }
        confirmText="Change mode"
        confirmValue={
          next ? `CHANGE TO ${productModeLabel[next].toUpperCase()}` : undefined
        }
        loading={change.isPending}
      >
        {next ? (
          <div
            className="space-y-2 rounded-md border bg-muted/30 p-3 text-xs"
            data-testid="charlie-mode-confirm-allowed"
          >
            <p className="font-medium text-foreground">
              What {productModeLabel[next]} allows
            </p>
            <ul className="list-disc space-y-1 pl-4 text-muted-foreground">
              {modeAllowedSummary[next].map((item) => (
                <li key={item}>{item}</li>
              ))}
            </ul>
            <p className="pt-1 text-muted-foreground">
              Current authoritative mode:{" "}
              <span className="font-medium text-foreground">
                {productModeLabel[m.authoritative]}
              </span>
              . Product agents will roll once to apply the new ceiling.
            </p>
          </div>
        ) : null}
      </ConfirmDialog>
      <ConfirmDialog
        open={emergency}
        onClose={() => setEmergency(false)}
        onConfirm={() => disable.mutate()}
        title="Emergency Disable Charlie"
        description={modeHelp.disabled}
        confirmText="Disable Charlie"
        confirmValue="DISABLE CHARLIE"
        variant="destructive"
        loading={disable.isPending}
      />
    </div>
  );
}
