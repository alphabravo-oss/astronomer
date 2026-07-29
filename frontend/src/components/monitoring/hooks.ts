/**
 * React-Query surface for the monitoring-stack LIFECYCLE — install, upgrade,
 * replace, uninstall, preview and status across the per-cluster stack, shared
 * Thanos and shared Alertmanager.
 *
 * Kept next to the monitoring UI rather than in the central lib/hooks.ts, the
 * same way components/projects/hooks.ts and components/auth/hooks.ts own their
 * feature's data layer. Cache keys still come from the central factory
 * (lib/query-keys.ts) so reads and invalidations cannot drift.
 *
 * THE POINT OF THIS FILE IS THE OPERATION TRACKER, not the mutations.
 *
 * Every mutating endpoint on this surface is asynchronous: it writes a
 * monitoring_operations row in `pending`, returns 202 with that row, and the
 * server-side reconciler does the Helm work afterwards — an install runs a
 * chart apply, then waits up to 2 minutes for pods to become ready, then up to
 * 90s for the service health probe, then a PromQL smoke query. Forty seconds is
 * a fast install. A UI that renders the 202 as success is wrong within a second
 * of the user clicking the button, so the tracker below is the primitive the
 * screens are built on: it follows one operation to a terminal state, adopts
 * work that was already in flight when the page loaded, surfaces the
 * reconciler's real error text, and offers retry.
 *
 * WHY IT POLLS UNCONDITIONALLY (and does not use `liveFallback`)
 *
 * The other operation queues publish SSE events — `tool_operation.changed`,
 * `logging_operation.changed` — so their hooks poll only while the live stream
 * is down. internal/events/bus.go has NO monitoring_operation type and
 * internal/handler/monitoring_operations.go publishes nothing, so for this
 * surface polling is the only signal there is. Wrapping the interval in
 * `liveFallback` here would switch tracking OFF for exactly the users whose
 * stream is healthy. If a monitoring_operation.changed event is ever added,
 * this is the line to change (and add the route in lib/live/routes.ts).
 */
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';

import { queryKeys } from '@/lib/query-keys';
import { toastApiError, toastSuccess } from '@/lib/toast';
import * as api from '@/lib/api/monitoring-stack';
import {
  isActiveOperationStatus,
  isRetryableOperationStatus,
  isTerminalOperationStatus,
  operationTargetOf,
  parseReplaceRequiredError,
  stackTargetLabel,
  type MonitoringOperation,
  type MonitoringOperationEvent,
  type MonitoringOperationType,
  type MonitoringStackRequestBody,
  type MonitoringStackStatusBase,
  type MonitoringStackTarget,
  type ReplaceRequiredError,
} from '@/lib/api/monitoring-stack';

// ============================================================
// Tracking cadence + ceilings
// ============================================================

/**
 * Poll cadence while an operation is pending or running. The reconciler ticks
 * every 30s but is kicked immediately on enqueue and writes stage events
 * throughout, so a ~2.5s poll keeps the stage log moving without hammering.
 */
export const MONITORING_OP_ACTIVE_POLL_MS = 2_500;

/**
 * How long an operation may stay non-terminal before the UI stops claiming it
 * is progressing normally. The slowest legitimate path — replace: uninstall,
 * install, 2 minutes of pod readiness, 90s health, 90s smoke — lands around 5
 * minutes per attempt, so 6 minutes without terminating means something is
 * genuinely wrong (agent disconnected mid-apply, reconciler wedged, the row
 * claimed by a process that died). At this point `isStalled` goes true and the
 * cadence relaxes.
 */
export const MONITORING_OP_STALL_AFTER_MS = 6 * 60_000;

/** Relaxed cadence once stalled — still tracking, just not every 2.5s. */
export const MONITORING_OP_STALLED_POLL_MS = 15_000;

/**
 * The client-side ceiling. After 30 minutes of a non-terminal operation the
 * tracker stops polling entirely and reports `hasStoppedTracking`. It does NOT
 * pretend the operation failed — it did not, and it may still complete
 * server-side — the UI says tracking stopped and offers Refresh (a manual
 * refetch) and Retry (which requeues the row and resumes tracking). Without
 * this ceiling a wedged operation leaves a page polling for as long as the tab
 * stays open.
 */
export const MONITORING_OP_TRACK_CEILING_MS = 30 * 60_000;

/**
 * A `failed` row is not necessarily final: the reconciler's OnFailure closure
 * marks the row failed and then, when attemptCount is still below the backend's
 * maxRetryAttempts policy, immediately requeues it to `pending` (see
 * claimPendingMonitoringOperations). Those two writes are milliseconds apart,
 * so the tracker keeps polling for a short grace window after a failure to see
 * whether the policy takes over. The failure is surfaced immediately either
 * way; what the window changes is whether the tracker declares it SETTLED.
 */
export const MONITORING_OP_FAILURE_SETTLE_MS = 8_000;

/** Poll cadence inside the failure grace window. */
export const MONITORING_OP_FAILURE_SETTLE_POLL_MS = 2_000;

/** Cadence of the adopt query while nothing is being tracked. */
export const MONITORING_OP_ADOPT_POLL_MS = 10_000;

/** How many recent rows the adopt query pulls for a target. */
const ADOPT_PAGE_SIZE = 5;

function timestampMs(value: string | null | undefined): number | null {
  if (!value) return null;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? null : parsed;
}

/**
 * Age of an operation, measured from when the reconciler claimed it, falling
 * back to when it was enqueued. Both come off the SERVER row, deliberately: a
 * page refresh, or a colleague's browser adopting the same operation, must
 * compute the same age as the tab that started it.
 */
export function monitoringOperationElapsedMs(
  op: MonitoringOperation | undefined | null,
  nowMs: number,
): number {
  if (!op) return 0;
  const started = timestampMs(op.startedAt) ?? timestampMs(op.createdAt);
  if (started === null) return 0;
  return Math.max(0, nowMs - started);
}

/**
 * The whole polling policy as one pure function of (row, now) — extracted so
 * every branch is unit-testable without a timer, and so the hook's
 * `refetchInterval` is a one-liner.
 *
 * Returns the milliseconds until the next poll, or `false` to stop polling.
 * React Query owns the timer, which is what makes unmount safe: tearing down
 * the observer cancels it. There is no setInterval anywhere in this file.
 */
export function monitoringOperationPollInterval(
  op: MonitoringOperation | undefined | null,
  nowMs: number,
): number | false {
  if (!op) return false;

  // Terminal and final: nothing more will happen to this row.
  if (op.status === 'completed' || op.status === 'superseded') return false;

  // Terminal, but the retry policy may requeue it within milliseconds.
  if (op.status === 'failed') {
    const settledAt = timestampMs(op.completedAt) ?? timestampMs(op.updatedAt);
    if (settledAt !== null && nowMs - settledAt < MONITORING_OP_FAILURE_SETTLE_MS) {
      return MONITORING_OP_FAILURE_SETTLE_POLL_MS;
    }
    return false;
  }

  // pending | running (or a status this client does not know — poll it, the
  // ceiling below still applies).
  const elapsed = monitoringOperationElapsedMs(op, nowMs);
  if (elapsed >= MONITORING_OP_TRACK_CEILING_MS) return false;
  if (elapsed >= MONITORING_OP_STALL_AFTER_MS) return MONITORING_OP_STALLED_POLL_MS;
  return MONITORING_OP_ACTIVE_POLL_MS;
}

/**
 * Milliseconds left in the auto-requeue grace window for a failed row, or 0
 * when it does not apply. The hook uses this to schedule the single re-render
 * that flips the card from "failed, may retry automatically" to "failed" —
 * React Query's structural sharing means an unchanged poll response does not
 * re-render, so without it the transition would never be drawn.
 */
export function failureSettleRemainingMs(
  op: MonitoringOperation | null | undefined,
  nowMs: number,
): number {
  if (!op || op.status !== 'failed') return 0;
  const settledAt = timestampMs(op.completedAt) ?? timestampMs(op.updatedAt);
  if (settledAt === null) return 0;
  return Math.max(0, MONITORING_OP_FAILURE_SETTLE_MS - (nowMs - settledAt));
}

/**
 * How often the elapsed clock is redrawn while an operation is in flight. The
 * tracker derives `elapsedMs` from Date.now() at RENDER time, and a poll that
 * returns a byte-identical row produces no render at all (React Query's
 * structural sharing hands back the same reference), so without this the
 * header reads "Install in progress · 0s" for the entire install.
 */
export const MONITORING_OP_ELAPSED_TICK_MS = 1_000;

/** Floor on a scheduled tick, so a boundary landing 2ms away cannot spin. */
const MIN_TICK_MS = 50;

/**
 * Milliseconds until the tracker's DERIVED surface next changes on its own —
 * the elapsed clock, `isStalled`, `hasStoppedTracking`, `isSettled` — or 0 when
 * nothing is pending.
 *
 * THIS IS WHAT MAKES THE TIME-BASED STATES REACHABLE. Every one of them is
 * computed at render time from `Date.now()`, but a WEDGED operation produces no
 * server-side change to re-render on: the polled row is identical, React Query
 * hands back the same reference, and the observer never notifies. Before this
 * existed, an operation stuck in `running` rendered a spinner and a frozen "0s"
 * forever — the stall notice, the 30-minute ceiling notice and the Refresh
 * button they carry were all unreachable, which is to say the entire
 * gave-up-tracking design was dead code.
 *
 * Pure and total so the schedule is unit-testable without a timer, the same way
 * monitoringOperationPollInterval is.
 */
export function nextTrackerTickMs(
  op: MonitoringOperation | null | undefined,
  nowMs: number,
): number {
  // A failure inside its grace window: one tick, when the window closes.
  const settleRemaining = failureSettleRemainingMs(op, nowMs);
  if (settleRemaining > 0) return settleRemaining;

  if (!isActiveOperationStatus(op?.status)) return 0;

  const elapsed = monitoringOperationElapsedMs(op, nowMs);
  // Past the ceiling nothing else changes without the user asking: polling has
  // stopped and there is no later boundary to cross.
  if (elapsed >= MONITORING_OP_TRACK_CEILING_MS) return 0;

  const remainingToBoundary = [MONITORING_OP_STALL_AFTER_MS, MONITORING_OP_TRACK_CEILING_MS]
    .map((boundary) => boundary - elapsed)
    .filter((remaining) => remaining > 0);

  // Tick for the clock, but never sail past a boundary.
  return Math.max(MIN_TICK_MS, Math.min(MONITORING_OP_ELAPSED_TICK_MS, ...remainingToBoundary));
}

/** True once a `failed` row is past the auto-requeue grace window. */
export function isSettledFailure(op: MonitoringOperation, nowMs: number): boolean {
  if (op.status === 'superseded') return true;
  if (op.status !== 'failed') return false;
  return failureSettleRemainingMs(op, nowMs) === 0;
}

/** Cache-key discriminant for one target's status query. */
export function stackFamilyKey(target: MonitoringStackTarget): string {
  return target.kind === 'cluster' ? `cluster:${target.clusterId}` : target.kind;
}

// ============================================================
// Status
// ============================================================

export function useClusterStackStatus(clusterId: string | undefined) {
  return useQuery({
    queryKey: queryKeys.monitoringStack.status(`cluster:${clusterId ?? ''}`),
    queryFn: () => api.getClusterStackStatus(clusterId as string),
    enabled: !!clusterId,
  });
}

export function useSharedThanosStatus(enabled = true) {
  return useQuery({
    queryKey: queryKeys.monitoringStack.status('thanos'),
    queryFn: () => api.getSharedThanosStatus(),
    enabled,
  });
}

export function useSharedAlertmanagerStatus(enabled = true) {
  return useQuery({
    queryKey: queryKeys.monitoringStack.status('alertmanager'),
    queryFn: () => api.getSharedAlertmanagerStatus(),
    enabled,
  });
}

/** Target-generic status query, for screens that render all three families. */
export function useStackStatus(target: MonitoringStackTarget, enabled = true) {
  const family = stackFamilyKey(target);
  const disabled = target.kind === 'cluster' && !target.clusterId;
  // `family` IS the identity of `target` (stackFamilyKey is total over the
  // union), so the key below is complete. Putting the target object in the key
  // instead would give this hook a different key from useClusterStackStatus /
  // useSharedThanosStatus, which must share the same cache entry.
  // eslint-disable-next-line @tanstack/query/exhaustive-deps
  return useQuery<MonitoringStackStatusBase>({
    queryKey: queryKeys.monitoringStack.status(family),
    queryFn: () => api.getStackStatus(target),
    enabled: enabled && !disabled,
  });
}

// ============================================================
// Operations list
// ============================================================

export function useMonitoringOperations(params?: api.MonitoringOperationListParams) {
  return useQuery({
    queryKey: queryKeys.monitoringStack.operations(params as Record<string, unknown>),
    queryFn: () => api.listMonitoringOperations(params),
  });
}

// ============================================================
// The tracker
// ============================================================

export interface MonitoringOperationTracking {
  /** The operation being followed, or null when nothing is in flight. */
  operation: MonitoringOperation | null;
  /** Newest operation for this target regardless of state — for "last run" UI. */
  latestOperation: MonitoringOperation | null;
  status: MonitoringOperation['status'] | 'idle';
  /** pending | running. */
  isActive: boolean;
  isTerminal: boolean;
  isSuccess: boolean;
  /** failed or superseded (surfaced immediately, before the settle window). */
  isFailure: boolean;
  /** Terminal AND past the auto-requeue grace window. */
  isSettled: boolean;
  /** Failed, but the backend's retry policy may still pick it up. */
  isAwaitingAutoRetry: boolean;
  /**
   * The reconciler's error text, VERBATIM. This is the actual Helm/readiness
   * failure ("release readiness check timed out: 1/3 ready pods for prometheus",
   * "... rollback to revision 4 completed"), and screens must render it as-is
   * rather than substituting a generic message.
   */
  errorMessage: string | null;
  /** The operation's stage log, oldest first. Detail endpoint only. */
  events: MonitoringOperationEvent[];
  attemptCount: number;
  elapsedMs: number;
  /** Non-terminal for longer than MONITORING_OP_STALL_AFTER_MS. */
  isStalled: boolean;
  /** Client ceiling hit: polling stopped, the operation is still non-terminal. */
  hasStoppedTracking: boolean;
  /**
   * True when an operation for this target is in flight — block new enqueues.
   *
   * Deliberately goes FALSE once `hasStoppedTracking` is true. Past the ceiling
   * the client no longer knows anything about this row, and the backend accepts
   * an enqueue over it (superseding it), so leaving the panel disabled would
   * strand an operator behind a wedged reconciler with no way out.
   */
  isBusy: boolean;
  /** True when the backend would accept a retry for this row. */
  canRetry: boolean;
  retry: () => void;
  isRetrying: boolean;
  /** Manual refetch — what the Refresh affordance calls after the ceiling. */
  refresh: () => void;
  /** Adopt an operation (e.g. the 202 receipt from an enqueue). */
  track: (op: MonitoringOperation | string) => void;
  /** Stop following the current operation without touching the server. */
  clear: () => void;
  isLoading: boolean;
}

export interface UseMonitoringOperationTrackerOptions {
  enabled?: boolean;
  /** Fired once, when an operation reaches a settled terminal state. */
  onSettled?: (op: MonitoringOperation) => void;
}

/**
 * Follow one monitoring operation for one target through to a terminal state.
 *
 * ADOPT-ON-LOAD. On mount the tracker asks ListOperations for this target's
 * newest rows (targetType + targetKey, limit 5, newest first) and, if the
 * newest is `pending` or `running`, starts following it. That covers both cases
 * the operator hits in practice: they refreshed mid-install, or a colleague
 * started one from another browser. Screens read `isBusy` and disable their
 * action buttons instead of offering to enqueue a second operation — which the
 * backend would accept and then supersede, cancelling the first and leaving
 * whoever started it looking at a row that says "superseded by newer operation
 * for target". The adopt query keeps polling at a slow cadence whenever nothing
 * is being tracked, so an operation a colleague starts while the page is open
 * is adopted too.
 */
export function useMonitoringOperationTracker(
  target: MonitoringStackTarget,
  options: UseMonitoringOperationTrackerOptions = {},
): MonitoringOperationTracking {
  const { enabled = true } = options;
  const queryClient = useQueryClient();
  const { targetType, targetKey } = operationTargetOf(target);
  const family = stackFamilyKey(target);

  const [trackedId, setTrackedId] = useState<string | null>(null);
  const [dismissedId, setDismissedId] = useState<string | null>(null);
  // Bumped when a failure's grace window closes; see the timer effect below.
  const [settleTick, bumpSettleTick] = useReducer((n: number) => n + 1, 0);
  const settledRef = useRef<string | null>(null);
  const onSettledRef = useRef(options.onSettled);
  onSettledRef.current = options.onSettled;

  const listParams = useMemo(
    () => ({ targetType, targetKey, limit: ADOPT_PAGE_SIZE }),
    [targetType, targetKey],
  );
  const listKey = useMemo(
    () => queryKeys.monitoringStack.operations(listParams),
    [listParams],
  );
  const detailKey = useMemo(
    () => queryKeys.monitoringStack.operation(trackedId ?? ''),
    [trackedId],
  );
  const statusKey = useMemo(() => queryKeys.monitoringStack.status(family), [family]);

  // ── Detail: the tracked row, with its stage events ──
  const detailQuery = useQuery({
    queryKey: detailKey,
    queryFn: () => api.getMonitoringOperation(trackedId as string),
    enabled: enabled && !!trackedId,
    // React Query owns this timer; unmounting the component removes the
    // observer and the timer with it. Nothing to clean up by hand.
    refetchInterval: (query) => monitoringOperationPollInterval(query.state.data, Date.now()),
  });

  const trackedOperation = detailQuery.data ?? null;
  const active = isActiveOperationStatus(trackedOperation?.status);

  // ── Adopt: newest rows for this target ──
  const listQuery = useQuery({
    queryKey: listKey,
    queryFn: () => api.listMonitoringOperations(listParams),
    enabled: enabled && !!targetKey,
    // While something is in flight the detail poll is authoritative; the adopt
    // query only needs to run when we are NOT following anything.
    refetchInterval: active ? false : MONITORING_OP_ADOPT_POLL_MS,
  });

  const latestOperation = listQuery.data?.[0] ?? null;

  /**
   * What the screen renders. While something is being followed that is the
   * tracked row; otherwise it falls back to this target's newest operation, so
   * that an operator arriving after a failed install sees WHY it failed and a
   * Retry button, rather than an empty panel with the truth one API call away.
   * `clear()` dismisses that fallback for the row the user dismissed.
   */
  const operation =
    trackedOperation ??
    (!trackedId && latestOperation && latestOperation.id !== dismissedId ? latestOperation : null);

  useEffect(() => {
    if (!enabled || !latestOperation) return;
    if (latestOperation.id === trackedId) return;
    // A row the operator explicitly dismissed stays dismissed. Without this the
    // next 10s adopt poll silently re-adopts a wedged operation the operator
    // just walked away from, undoing `clear()` and re-locking the panel.
    if (latestOperation.id === dismissedId) return;
    if (!isActiveOperationStatus(latestOperation.status)) return;
    // Seed the detail cache so the UI shows the adopted row immediately rather
    // than flashing empty for one round-trip. The list row has no events; the
    // first detail poll fills them in.
    queryClient.setQueryData<MonitoringOperation>(
      queryKeys.monitoringStack.operation(latestOperation.id),
      (prev) => prev ?? latestOperation,
    );
    settledRef.current = null;
    setTrackedId(latestOperation.id);
  }, [enabled, latestOperation, trackedId, dismissedId, queryClient]);

  // ── Settle: refresh the stack's status once the operation finishes ──
  //
  // Without this the status card keeps showing "installing" after the install
  // completed, until the user navigates away and back.
  // Keyed off the TRACKED row only: the fallback above is a row we merely
  // found on load, and re-invalidating on every mount because the last install
  // finished yesterday would be noise.
  useEffect(() => {
    if (!trackedOperation || !isTerminalOperationStatus(trackedOperation.status)) return;
    if (trackedOperation.status !== 'completed' && !isSettledFailure(trackedOperation, Date.now())) {
      return;
    }
    const stamp = `${trackedOperation.id}:${trackedOperation.status}:${trackedOperation.updatedAt}`;
    if (settledRef.current === stamp) return;
    settledRef.current = stamp;
    queryClient.invalidateQueries({ queryKey: statusKey });
    queryClient.invalidateQueries({ queryKey: listKey });
    onSettledRef.current?.(trackedOperation);
    // `settleTick` is a real dependency: a failure inside its grace window
    // returns above, and the tick is the only thing that re-runs this effect
    // once the window closes (the polled row itself is byte-identical, so
    // React Query hands back the same reference).
  }, [trackedOperation, queryClient, statusKey, listKey, settleTick]);

  // One timer, cleared on unmount or when the row changes: re-render exactly
  // when a time-derived state next changes — the failure grace window closing,
  // the stall threshold, the tracking ceiling, or simply the next second of the
  // elapsed clock. Nothing else would: the poll that crosses any of those
  // boundaries returns identical data, which React Query's structural sharing
  // turns into no state change at all.
  //
  // `settleTick` is a real dependency. The timer's own bump is what re-runs
  // this effect to schedule the NEXT tick; without it the tracker would move
  // exactly once and freeze again.
  useEffect(() => {
    const delay = nextTrackerTickMs(operation, Date.now());
    if (delay <= 0) return;
    const timer = setTimeout(() => bumpSettleTick(), delay);
    return () => clearTimeout(timer);
  }, [operation, settleTick]);

  const retryMutation = useMutation({
    mutationFn: (id: string) => api.retryMonitoringOperation(id),
    onSuccess: (requeued) => {
      // Retry requeues IN PLACE — same row, same id, status back to `pending`,
      // error cleared — so tracking simply continues. The retry response
      // carries no events, so keep the ones already fetched.
      queryClient.setQueryData<MonitoringOperation>(
        queryKeys.monitoringStack.operation(requeued.id),
        (prev) => (prev ? { ...prev, ...requeued, events: prev.events } : requeued),
      );
      settledRef.current = null;
      setTrackedId(requeued.id);
      queryClient.invalidateQueries({ queryKey: listKey });
      toastSuccess('Monitoring operation requeued');
    },
    onError: (err: Error) => {
      toastApiError('Failed to retry operation', err);
    },
  });

  const track = useCallback(
    (op: MonitoringOperation | string) => {
      const id = typeof op === 'string' ? op : op.id;
      if (typeof op !== 'string') {
        queryClient.setQueryData<MonitoringOperation>(
          queryKeys.monitoringStack.operation(id),
          (prev) => (prev ? { ...prev, ...op, events: op.events ?? prev.events } : op),
        );
      }
      settledRef.current = null;
      setDismissedId(null);
      setTrackedId(id);
      queryClient.invalidateQueries({ queryKey: listKey });
    },
    [queryClient, listKey],
  );

  // Read through a ref so `clear` and `retry` keep stable identities across the
  // poll's re-renders.
  const operationRef = useRef(operation);
  operationRef.current = operation;

  const clear = useCallback(() => {
    settledRef.current = null;
    setDismissedId(operationRef.current?.id ?? null);
    setTrackedId(null);
  }, []);

  // Destructured because a query/mutation result object is not referentially
  // stable; `refetch` and `mutate` are.
  const { refetch: refetchDetail } = detailQuery;
  const { refetch: refetchList } = listQuery;
  const { mutate: requeueOperation, isPending: isRetrying } = retryMutation;

  const refresh = useCallback(() => {
    void refetchDetail();
    void refetchList();
  }, [refetchDetail, refetchList]);

  const retry = useCallback(() => {
    const id = operationRef.current?.id;
    if (!id || isRetrying) return;
    requeueOperation(id);
  }, [requeueOperation, isRetrying]);

  const now = Date.now();
  const elapsed = monitoringOperationElapsedMs(operation, now);
  const terminal = isTerminalOperationStatus(operation?.status);
  const failure = operation?.status === 'failed' || operation?.status === 'superseded';
  const settled =
    !!operation && terminal && (operation.status === 'completed' || isSettledFailure(operation, now));
  const errorMessage = operation?.errorMessage?.trim() ? operation.errorMessage : null;
  // `active` above governs the ADOPT query's cadence and is deliberately keyed
  // off the tracked row; the surface below reports on whatever the screen is
  // showing, which may still be the un-adopted fallback for one render.
  const displayActive = isActiveOperationStatus(operation?.status);
  // Past the ceiling the client has stopped following this row. It is NOT
  // evidence that work is still in flight — a wedged reconciler leaves a
  // pending/running row behind indefinitely — so it must not keep the panel's
  // controls disabled. The backend deliberately accepts an enqueue over an
  // active operation and supersedes the old one, which is exactly the escape an
  // operator with a stuck stack needs; `isBusy` going false is what offers it.
  const stoppedTracking = displayActive && elapsed >= MONITORING_OP_TRACK_CEILING_MS;

  return {
    operation,
    latestOperation,
    status: operation?.status ?? 'idle',
    isActive: displayActive,
    isTerminal: terminal,
    isSuccess: operation?.status === 'completed',
    isFailure: failure,
    isSettled: settled,
    isAwaitingAutoRetry: operation?.status === 'failed' && !settled,
    errorMessage,
    events: operation?.events ?? [],
    attemptCount: operation?.attemptCount ?? 0,
    elapsedMs: elapsed,
    isStalled: displayActive && elapsed >= MONITORING_OP_STALL_AFTER_MS,
    hasStoppedTracking: stoppedTracking,
    isBusy: displayActive && !stoppedTracking,
    canRetry: isRetryableOperationStatus(operation?.status),
    retry,
    isRetrying,
    refresh,
    track,
    clear,
    isLoading: detailQuery.isLoading || listQuery.isLoading,
  };
}

// ============================================================
// Preview + lifecycle
// ============================================================

export function useStackPreview(target: MonitoringStackTarget) {
  return useMutation({
    mutationFn: (body: MonitoringStackRequestBody) => api.previewStack(target, body),
    onError: (err: Error) => {
      toastApiError('Failed to render monitoring stack preview', err);
    },
  });
}

export interface MonitoringStackController {
  status: ReturnType<typeof useStackStatus>;
  tracker: MonitoringOperationTracking;
  preview: ReturnType<typeof useStackPreview>;
  /**
   * Enqueue a lifecycle verb. Resolves with the 202 receipt (already handed to
   * the tracker) or null when the request was rejected — errors are surfaced
   * through the toast and, for the 409 case, through `replaceRequired`. It
   * never rejects, so callers do not need a try/catch to avoid an unhandled
   * rejection.
   */
  run: (verb: MonitoringOperationType, body?: MonitoringStackRequestBody) =>
    Promise<MonitoringOperation | null>;
  /** An enqueue request is in flight (not the operation itself — that is tracker.isActive). */
  isEnqueuing: boolean;
  /** Enqueuing, or an operation is already running: every action should be disabled. */
  isBusy: boolean;
  /** Set when upgrade 409s because the change needs a reinstall. */
  replaceRequired: ReplaceRequiredError | null;
  clearReplaceRequired: () => void;
}

/**
 * The composed surface a lifecycle screen should use: status + tracker +
 * preview + the four verbs, wired so an enqueue is ALWAYS handed to the
 * tracker. Calling the individual hooks is fine, but this is the shape that
 * makes "fire and forget" impossible to write by accident.
 *
 * NOT BUILT HERE, deliberately: Helm release history, a revision picker and a
 * values diff. That is the separate open audit item
 * `no-release-history-or-revision-rollback-ui`, and these screens are its
 * natural home — the backend already tracks `lastObservedRevision` on the
 * status response and the reconciler rolls back to a previous revision itself
 * (rollbackIfConfigured). It is out of scope for this change.
 */
export function useMonitoringStackController(
  target: MonitoringStackTarget,
  options: { enabled?: boolean } = {},
): MonitoringStackController {
  const { enabled = true } = options;
  const queryClient = useQueryClient();
  const status = useStackStatus(target, enabled);
  const tracker = useMonitoringOperationTracker(target, { enabled });
  const preview = useStackPreview(target);
  const [replaceRequired, setReplaceRequired] = useState<ReplaceRequiredError | null>(null);
  const family = stackFamilyKey(target);

  const trackRef = useRef(tracker.track);
  trackRef.current = tracker.track;

  const lifecycle = useMutation({
    mutationFn: ({ verb, body }: { verb: MonitoringOperationType; body?: MonitoringStackRequestBody }) =>
      api.runStackLifecycle(target, verb, body),
    onSuccess: (op, { verb }) => {
      trackRef.current(op);
      // The handler persists the new desired state (status "installing" /
      // "updating" / "uninstalled") before enqueueing, so the status card is
      // already stale by the time we get here.
      queryClient.invalidateQueries({ queryKey: queryKeys.monitoringStack.status(family) });
      toastSuccess(`Queued ${verb} for ${stackTargetLabel(target)}`);
    },
    onError: (err: Error, { verb }) => {
      const conflict = parseReplaceRequiredError(err);
      if (conflict) {
        setReplaceRequired(conflict);
        return;
      }
      toastApiError(`Failed to queue ${verb}`, err);
    },
  });

  const { mutateAsync: enqueueLifecycle, isPending: isEnqueuing } = lifecycle;

  const run = useCallback(
    async (verb: MonitoringOperationType, body?: MonitoringStackRequestBody) => {
      setReplaceRequired(null);
      try {
        return await enqueueLifecycle({ verb, body });
      } catch {
        // Already surfaced by onError (toast or replaceRequired).
        return null;
      }
    },
    [enqueueLifecycle],
  );

  const clearReplaceRequired = useCallback(() => setReplaceRequired(null), []);

  return {
    status,
    tracker,
    preview,
    run,
    isEnqueuing,
    isBusy: isEnqueuing || tracker.isBusy,
    replaceRequired,
    clearReplaceRequired,
  };
}
