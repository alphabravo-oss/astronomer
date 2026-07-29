/**
 * Tests for the monitoring-stack operation tracker.
 *
 * The interesting behaviour here is not "does the request fire" — it is what
 * the UI does across the tens of seconds a Helm install actually takes: it must
 * keep following the operation, adopt one that is already in flight, show the
 * reconciler's real error, requeue on retry, and stop cleanly when the screen
 * goes away.
 */
import { act, renderHook } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('@/lib/toast', () => ({
  toastSuccess: vi.fn(),
  toastApiError: vi.fn(),
  toastError: vi.fn(),
  toastInfo: vi.fn(),
  toastWarning: vi.fn(),
  formatToastError: vi.fn(),
}));

vi.mock('@/lib/api/monitoring-stack', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/api/monitoring-stack')>();
  return {
    ...actual,
    getMonitoringOperation: vi.fn(),
    listMonitoringOperations: vi.fn(),
    retryMonitoringOperation: vi.fn(),
    getStackStatus: vi.fn(),
    runStackLifecycle: vi.fn(),
  };
});

import {
  getMonitoringOperation,
  listMonitoringOperations,
  retryMonitoringOperation,
  type MonitoringOperation,
} from '@/lib/api/monitoring-stack';
import {
  MONITORING_OP_ACTIVE_POLL_MS,
  MONITORING_OP_ADOPT_POLL_MS,
  MONITORING_OP_ELAPSED_TICK_MS,
  MONITORING_OP_FAILURE_SETTLE_MS,
  MONITORING_OP_FAILURE_SETTLE_POLL_MS,
  MONITORING_OP_STALL_AFTER_MS,
  MONITORING_OP_STALLED_POLL_MS,
  MONITORING_OP_TRACK_CEILING_MS,
  monitoringOperationPollInterval,
  nextTrackerTickMs,
  useMonitoringOperationTracker,
} from '@/components/monitoring/hooks';
import { queryKeys } from '@/lib/query-keys';

const getOperation = vi.mocked(getMonitoringOperation);
const listOperations = vi.mocked(listMonitoringOperations);
const retryOperation = vi.mocked(retryMonitoringOperation);

const CLUSTER_TARGET = { kind: 'cluster', clusterId: 'cluster-1' } as const;

function operation(overrides: Partial<MonitoringOperation> = {}): MonitoringOperation {
  const now = new Date(Date.now()).toISOString();
  return {
    id: 'op-1',
    targetType: 'cluster_stack',
    targetKey: 'cluster-1',
    operationType: 'install',
    status: 'running',
    attemptCount: 1,
    startedAt: now,
    completedAt: null,
    errorMessage: '',
    createdAt: now,
    updatedAt: now,
    ...overrides,
  };
}

function makeClient() {
  return new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 }, mutations: { retry: false } },
  });
}

function wrapperFor(client: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

/** Advance both the fake clock and React's work queue. */
async function tick(ms = 0) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.useFakeTimers();
  listOperations.mockResolvedValue([]);
  getOperation.mockResolvedValue(operation());
});

afterEach(() => {
  vi.useRealTimers();
});

// ─────────────────────────────────────────────────────────────────────
// The polling policy, as a pure function
// ─────────────────────────────────────────────────────────────────────

describe('monitoringOperationPollInterval', () => {
  const now = Date.parse('2026-07-29T12:00:00Z');
  const at = (msAgo: number) => new Date(now - msAgo).toISOString();

  it('polls a freshly started operation on the active cadence', () => {
    expect(
      monitoringOperationPollInterval(operation({ status: 'running', startedAt: at(5_000) }), now),
    ).toBe(MONITORING_OP_ACTIVE_POLL_MS);
    expect(
      monitoringOperationPollInterval(
        operation({ status: 'pending', startedAt: null, createdAt: at(1_000) }),
        now,
      ),
    ).toBe(MONITORING_OP_ACTIVE_POLL_MS);
  });

  it('relaxes the cadence once the operation is past the stall threshold', () => {
    expect(
      monitoringOperationPollInterval(
        operation({ status: 'running', startedAt: at(MONITORING_OP_STALL_AFTER_MS + 1_000) }),
        now,
      ),
    ).toBe(MONITORING_OP_STALLED_POLL_MS);
  });

  it('stops at the client ceiling rather than polling a wedged operation forever', () => {
    expect(
      monitoringOperationPollInterval(
        operation({ status: 'running', startedAt: at(MONITORING_OP_TRACK_CEILING_MS + 1) }),
        now,
      ),
    ).toBe(false);
  });

  it('stops immediately on completed and superseded', () => {
    expect(monitoringOperationPollInterval(operation({ status: 'completed' }), now)).toBe(false);
    expect(monitoringOperationPollInterval(operation({ status: 'superseded' }), now)).toBe(false);
  });

  it('keeps polling briefly after a failure, in case the retry policy requeues it', () => {
    // The reconciler marks a row failed and then requeues it to `pending`
    // milliseconds later when attemptCount < maxRetryAttempts.
    expect(
      monitoringOperationPollInterval(
        operation({ status: 'failed', completedAt: at(500), updatedAt: at(500) }),
        now,
      ),
    ).toBe(MONITORING_OP_FAILURE_SETTLE_POLL_MS);
    expect(
      monitoringOperationPollInterval(
        operation({
          status: 'failed',
          completedAt: at(MONITORING_OP_FAILURE_SETTLE_MS + 1_000),
          updatedAt: at(MONITORING_OP_FAILURE_SETTLE_MS + 1_000),
        }),
        now,
      ),
    ).toBe(false);
  });

  it('does nothing when there is no operation', () => {
    expect(monitoringOperationPollInterval(null, now)).toBe(false);
  });
});

// ─────────────────────────────────────────────────────────────────────
// The re-render schedule, as a pure function
// ─────────────────────────────────────────────────────────────────────

describe('nextTrackerTickMs', () => {
  const now = Date.parse('2026-07-29T12:00:00Z');
  const at = (msAgo: number) => new Date(now - msAgo).toISOString();

  it('schedules nothing for a terminal, settled row', () => {
    expect(nextTrackerTickMs(operation({ status: 'completed' }), now)).toBe(0);
    expect(nextTrackerTickMs(operation({ status: 'superseded' }), now)).toBe(0);
    expect(nextTrackerTickMs(null, now)).toBe(0);
  });

  it('schedules the close of a failure grace window', () => {
    expect(
      nextTrackerTickMs(
        operation({ status: 'failed', completedAt: at(1_000), updatedAt: at(1_000) }),
        now,
      ),
    ).toBe(MONITORING_OP_FAILURE_SETTLE_MS - 1_000);
  });

  it('ticks the elapsed clock while an operation is in flight', () => {
    expect(nextTrackerTickMs(operation({ status: 'running', startedAt: at(5_000) }), now)).toBe(
      MONITORING_OP_ELAPSED_TICK_MS,
    );
  });

  it('lands exactly on the stall and ceiling boundaries rather than sailing past', () => {
    // 400ms short of the stall threshold: tick then, not a full second later.
    expect(
      nextTrackerTickMs(
        operation({ status: 'running', startedAt: at(MONITORING_OP_STALL_AFTER_MS - 400) }),
        now,
      ),
    ).toBe(400);
    expect(
      nextTrackerTickMs(
        operation({ status: 'running', startedAt: at(MONITORING_OP_TRACK_CEILING_MS - 300) }),
        now,
      ),
    ).toBe(300);
  });

  it('stops scheduling past the ceiling — nothing further changes unprompted', () => {
    expect(
      nextTrackerTickMs(
        operation({ status: 'running', startedAt: at(MONITORING_OP_TRACK_CEILING_MS + 1) }),
        now,
      ),
    ).toBe(0);
  });
});

// ─────────────────────────────────────────────────────────────────────
// The hook
// ─────────────────────────────────────────────────────────────────────

describe('useMonitoringOperationTracker', () => {
  it('adopts an operation that was already in flight when the page loaded', async () => {
    // The operator refreshed mid-install, or a colleague started it. The list
    // endpoint is the only thing that can tell us.
    const inFlight = operation({ id: 'op-adopted', status: 'running' });
    listOperations.mockResolvedValue([inFlight]);
    getOperation.mockResolvedValue(inFlight);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    expect(listOperations).toHaveBeenCalledWith({
      targetType: 'cluster_stack',
      targetKey: 'cluster-1',
      limit: 5,
    });
    expect(result.current.operation?.id).toBe('op-adopted');
    expect(result.current.isActive).toBe(true);
    // The screen must disable its action buttons rather than enqueue a second
    // operation that would supersede this one.
    expect(result.current.isBusy).toBe(true);
    expect(getOperation).toHaveBeenCalledWith('op-adopted');
  });

  it('shows the newest terminal operation on load without treating it as in flight', async () => {
    listOperations.mockResolvedValue([operation({ id: 'op-old', status: 'completed' })]);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    expect(result.current.operation?.id).toBe('op-old');
    expect(result.current.latestOperation?.id).toBe('op-old');
    // Nothing is in flight, so the screen's action buttons stay enabled and no
    // detail poll is started.
    expect(result.current.isBusy).toBe(false);
    expect(result.current.isActive).toBe(false);
    expect(getOperation).not.toHaveBeenCalled();

    act(() => result.current.clear());
    await tick();
    expect(result.current.operation).toBeNull();
  });

  it('tracks an enqueued operation through to completion and refreshes the stack status', async () => {
    const client = makeClient();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    // The 202 receipt from an install: pending, no events yet.
    const enqueued = operation({ status: 'pending', startedAt: null });
    getOperation.mockResolvedValue(enqueued);
    act(() => result.current.track(enqueued));
    await tick();
    expect(result.current.status).toBe('pending');
    expect(result.current.isTerminal).toBe(false);

    // Reconciler claims it.
    getOperation.mockResolvedValue(operation({ status: 'running', attemptCount: 1 }));
    await tick(MONITORING_OP_ACTIVE_POLL_MS + 100);
    expect(result.current.status).toBe('running');

    // ...and finishes.
    invalidate.mockClear();
    getOperation.mockResolvedValue(
      operation({ status: 'completed', completedAt: new Date(Date.now()).toISOString() }),
    );
    await tick(MONITORING_OP_ACTIVE_POLL_MS + 100);

    expect(result.current.isSuccess).toBe(true);
    expect(result.current.isTerminal).toBe(true);
    expect(result.current.isBusy).toBe(false);
    expect(result.current.errorMessage).toBeNull();
    // The status card would otherwise sit on "installing" forever.
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: queryKeys.monitoringStack.status('cluster:cluster-1'),
    });

    // Terminal means terminal: no further polling.
    const callsAfterCompletion = getOperation.mock.calls.length;
    await tick(60_000);
    expect(getOperation.mock.calls.length).toBe(callsAfterCompletion);
  });

  it('surfaces the reconciler failure message verbatim and offers retry once settled', async () => {
    const failedAt = new Date(Date.now()).toISOString();
    const failed = operation({
      status: 'failed',
      attemptCount: 1,
      completedAt: failedAt,
      updatedAt: failedAt,
      errorMessage:
        'release readiness check timed out: 1/3 ready pods for prometheus; rollback to revision 4 completed',
    });
    // The list row (what a page load sees) carries no events; the detail
    // endpoint is the only source of the reconciler's stage log.
    listOperations.mockResolvedValue([failed]);
    getOperation.mockResolvedValue({
      ...failed,
      events: [
        {
          id: 'ev-1',
          level: 'error',
          stage: 'complete',
          message: 'operation failed',
          createdAt: failedAt,
        },
      ],
    });

    const client = makeClient();
    const invalidate = vi.spyOn(client, 'invalidateQueries');
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    // No "something went wrong": the operator gets the reconciler's own text.
    expect(result.current.errorMessage).toBe(
      'release readiness check timed out: 1/3 ready pods for prometheus; rollback to revision 4 completed',
    );
    expect(result.current.isFailure).toBe(true);
    // Opening the operation pulls in the stage log.
    act(() => result.current.track(failed));
    await tick();
    expect(result.current.events).toHaveLength(1);
    // Inside the grace window the backend's own retry policy may still requeue.
    expect(result.current.isAwaitingAutoRetry).toBe(true);
    expect(result.current.isSettled).toBe(false);

    invalidate.mockClear();
    await tick(MONITORING_OP_FAILURE_SETTLE_MS + 1_000);
    expect(result.current.isSettled).toBe(true);
    expect(result.current.isAwaitingAutoRetry).toBe(false);
    expect(result.current.canRetry).toBe(true);
    // Settling refreshes the stack status even though the polled row is
    // byte-identical to the previous poll.
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: queryKeys.monitoringStack.status('cluster:cluster-1'),
    });
  });

  it('reports a superseded operation as a retryable failure with the backend reason', async () => {
    const superseded = operation({
      status: 'superseded',
      errorMessage: 'superseded by newer operation for target',
    });
    listOperations.mockResolvedValue([superseded]);
    getOperation.mockResolvedValue(superseded);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    // Not adopted (terminal), but a screen that tracked it sees the reason.
    act(() => result.current.track(superseded));
    await tick();
    expect(result.current.isFailure).toBe(true);
    expect(result.current.isSettled).toBe(true);
    expect(result.current.canRetry).toBe(true);
    expect(result.current.errorMessage).toBe('superseded by newer operation for target');
  });

  it('retry requeues the same row in place and resumes tracking it', async () => {
    const failedAt = new Date(Date.now() - MONITORING_OP_FAILURE_SETTLE_MS - 1_000).toISOString();
    const failed = operation({
      status: 'failed',
      attemptCount: 1,
      completedAt: failedAt,
      updatedAt: failedAt,
      errorMessage: 'helm upgrade failed: timed out waiting for the condition',
    });
    listOperations.mockResolvedValue([failed]);
    getOperation.mockResolvedValue(failed);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();
    act(() => result.current.track(failed));
    await tick();
    expect(result.current.canRetry).toBe(true);

    // The backend requeues IN PLACE: same id, status pending, error cleared.
    const requeued = operation({
      status: 'pending',
      attemptCount: 1,
      startedAt: null,
      completedAt: null,
      errorMessage: '',
    });
    retryOperation.mockResolvedValue(requeued);
    getOperation.mockResolvedValue(requeued);

    act(() => result.current.retry());
    await tick();

    expect(retryOperation).toHaveBeenCalledWith('op-1');
    expect(result.current.status).toBe('pending');
    expect(result.current.isActive).toBe(true);
    expect(result.current.errorMessage).toBeNull();
    expect(result.current.isFailure).toBe(false);
  });

  it('stops polling when the screen unmounts mid-operation', async () => {
    const running = operation({ status: 'running' });
    listOperations.mockResolvedValue([running]);
    getOperation.mockResolvedValue(running);

    const client = makeClient();
    const { result, unmount } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();
    expect(result.current.isActive).toBe(true);

    await tick(MONITORING_OP_ACTIVE_POLL_MS * 2 + 200);
    const callsWhileMounted = getOperation.mock.calls.length;
    expect(callsWhileMounted).toBeGreaterThan(1);

    unmount();
    await tick(MONITORING_OP_ACTIVE_POLL_MS * 10);

    // React Query owns the timer and tears it down with the observer — a
    // leaked interval on a lifecycle screen is exactly the bug this asserts.
    expect(getOperation.mock.calls.length).toBe(callsWhileMounted);
  });

  it('gives up at the client ceiling without claiming the operation failed', async () => {
    const stuck = operation({
      status: 'running',
      startedAt: new Date(Date.now() - MONITORING_OP_TRACK_CEILING_MS - 1_000).toISOString(),
    });
    listOperations.mockResolvedValue([stuck]);
    getOperation.mockResolvedValue(stuck);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    expect(result.current.isStalled).toBe(true);
    expect(result.current.hasStoppedTracking).toBe(true);
    // Still `running`, not failed: the server may yet finish it.
    expect(result.current.isFailure).toBe(false);
    expect(result.current.status).toBe('running');

    // ...and the panel UNLOCKS. A wedged reconciler must not disable
    // Install/Upgrade/Replace/Uninstall forever: the backend accepts an enqueue
    // over an active row and supersedes it, which is the operator's only exit.
    // Retry stays unavailable — the backend 409s a non-terminal row.
    expect(result.current.isBusy).toBe(false);
    expect(result.current.canRetry).toBe(false);

    const callsAtCeiling = getOperation.mock.calls.length;
    await tick(MONITORING_OP_STALLED_POLL_MS * 4);
    expect(getOperation.mock.calls.length).toBe(callsAtCeiling);

    // ...and the user can still ask explicitly.
    act(() => result.current.refresh());
    await tick();
    expect(getOperation.mock.calls.length).toBeGreaterThan(callsAtCeiling);
  });

  it('crosses the stall and ceiling thresholds on a row the server never changes', async () => {
    // THE REGRESSION THIS PINS: every time-derived state is computed from
    // Date.now() at render time, and a wedged operation gives React Query
    // nothing to re-render on — the polled row is byte-identical, structural
    // sharing returns the same reference, the observer never notifies. Before
    // the tick timer existed this hook froze at its mount values, so the
    // spinner said "0s" forever and the stall / ceiling notices and their
    // Refresh button could not be reached at all.
    //
    // Mounting FRESH is the whole point: the previous test mounts a row already
    // past the ceiling, so it only ever asserts the first render.
    const fresh = operation({ status: 'running', startedAt: new Date(Date.now()).toISOString() });
    listOperations.mockResolvedValue([fresh]);
    getOperation.mockResolvedValue(fresh);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();

    expect(result.current.isStalled).toBe(false);
    expect(result.current.hasStoppedTracking).toBe(false);
    expect(result.current.isBusy).toBe(true);

    // The elapsed clock moves without any server-side change.
    await tick(5_000);
    expect(result.current.elapsedMs).toBeGreaterThanOrEqual(5_000);

    // Stall threshold crossed — the "still running after N" notice becomes
    // renderable.
    await tick(MONITORING_OP_STALL_AFTER_MS);
    expect(result.current.isStalled).toBe(true);
    expect(result.current.hasStoppedTracking).toBe(false);
    expect(result.current.isBusy).toBe(true);

    // ...then the ceiling, which is what unlocks the panel's controls.
    await tick(MONITORING_OP_TRACK_CEILING_MS);
    expect(result.current.hasStoppedTracking).toBe(true);
    expect(result.current.isBusy).toBe(false);
    expect(result.current.status).toBe('running');
  });

  it('does not re-adopt an operation the operator dismissed', async () => {
    // clear() is the only way off a wedged row, and the adopt query re-runs
    // every 10s — without the dismissal guard it would silently take the same
    // operation back and re-lock the panel.
    const stuck = operation({
      id: 'op-stuck',
      status: 'running',
      startedAt: new Date(Date.now() - MONITORING_OP_TRACK_CEILING_MS - 1_000).toISOString(),
    });
    listOperations.mockResolvedValue([stuck]);
    getOperation.mockResolvedValue(stuck);

    const client = makeClient();
    const { result } = renderHook(() => useMonitoringOperationTracker(CLUSTER_TARGET), {
      wrapper: wrapperFor(client),
    });
    await tick();
    expect(result.current.operation?.id).toBe('op-stuck');

    act(() => result.current.clear());
    await tick();
    expect(result.current.operation).toBeNull();

    // Several adopt polls later it is still gone.
    await tick(MONITORING_OP_ADOPT_POLL_MS * 3 + 500);
    expect(result.current.operation).toBeNull();
    expect(result.current.isBusy).toBe(false);

    // A genuinely NEW operation is still adopted — the guard suppresses one id,
    // not adoption itself. (Two flushes: the adopt poll seeds the detail cache,
    // and this test's gcTime:0 client drops the seed before an observer
    // attaches, so the detail fetch lands on the following tick.)
    const replacement = operation({ id: 'op-new', status: 'pending', startedAt: null });
    listOperations.mockResolvedValue([replacement]);
    getOperation.mockResolvedValue(replacement);
    await tick(MONITORING_OP_ADOPT_POLL_MS + 500);
    await tick();
    expect(result.current.operation?.id).toBe('op-new');
    expect(result.current.isBusy).toBe(true);
  });
});
