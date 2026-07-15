import type { QueryClient } from '@tanstack/react-query';

// Each connect mints a fresh single-use ticket; number them so tests can
// assert re-mint-per-(re)connect.
vi.mock('@/lib/api', () => {
  let n = 0;
  return {
    createStreamTicket: vi.fn(() => {
      n += 1;
      return Promise.resolve({ ticket: `tkt-${n}` });
    }),
  };
});

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  close(): void {
    this.closed = true;
  }
  emitOpen(): void {
    this.onopen?.();
  }
  emitFrame(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

/**
 * Fresh module graph per test — the transport is a module-level singleton.
 * The '@/lib/api' mock instance is cached across resetModules, so clear its
 * call log here (the ticket counter keeps incrementing, which is fine — the
 * tests only assert that tickets differ per (re)connect).
 */
async function loadLive() {
  vi.resetModules();
  const api = await import('@/lib/api');
  const createStreamTicket = vi.mocked(api.createStreamTicket);
  createStreamTicket.mockClear();
  const stream = await import('@/lib/live/stream');
  const status = await import('@/lib/live/status-store');
  return { createStreamTicket, stream, status };
}

/** Run pending microtasks (ticket promise resolution) under fake timers. */
const flush = () => vi.advanceTimersByTimeAsync(0);

function fakeQueryClient() {
  return { invalidateQueries: vi.fn() } as unknown as QueryClient & {
    invalidateQueries: ReturnType<typeof vi.fn>;
  };
}

beforeEach(() => {
  vi.useFakeTimers();
  FakeEventSource.instances = [];
  (globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource;
});

afterEach(() => {
  vi.useRealTimers();
});

describe('live stream transport', () => {
  it('dispatches default-framed events by envelope type with camelized data', async () => {
    const { createStreamTicket, stream } = await loadLive();
    stream.acquireLiveStream();
    await flush();
    expect(createStreamTicket).toHaveBeenCalledTimes(1);
    expect(createStreamTicket).toHaveBeenCalledWith('events');

    const es = FakeEventSource.instances[0];
    expect(es.url).toContain('/events/stream/?ticket=tkt-');
    es.emitOpen();

    const seen: unknown[] = [];
    const wildcard: unknown[] = [];
    stream.liveTarget().addEventListener('cluster.registration.step', (e) => {
      seen.push((e as CustomEvent).detail);
    });
    stream.liveTarget().addEventListener('*', (e) => {
      wildcard.push((e as CustomEvent).detail);
    });

    // No `event:` line on the wire — this frame arrives via onmessage and is
    // routed purely on the JSON envelope's `type` (the old KNOWN_EVENT_TYPES
    // registration footgun is gone).
    es.emitFrame({
      id: 7,
      type: 'cluster.registration.step',
      time: '2026-07-15T00:00:00Z',
      data: { cluster_id: 'c1', step_name: 'apply' },
    });

    expect(seen).toEqual([
      {
        id: 7,
        type: 'cluster.registration.step',
        time: '2026-07-15T00:00:00Z',
        data: { clusterId: 'c1', stepName: 'apply' },
      },
    ]);
    expect(wildcard).toHaveLength(1);
    stream.releaseLiveStream();
  });

  it('does not invalidate on first open; invalidates on drop and on reconnect', async () => {
    const { createStreamTicket, stream } = await loadLive();
    const qc = fakeQueryClient();
    stream.registerLiveQueryClient(qc);

    stream.acquireLiveStream();
    await flush();
    FakeEventSource.instances[0].emitOpen();
    // First open: queries are fetching fresh already — no catch-up needed.
    expect(qc.invalidateQueries).not.toHaveBeenCalled();

    // open→closed kick: fallback polling must restart, which requires every
    // mounted query's refetchInterval fn to re-evaluate (post-fetch only).
    FakeEventSource.instances[0].onerror?.();
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({ refetchType: 'active' });

    // Backoff (1s) then re-mint + reconnect: single-use tickets mean every
    // (re)connect mints a fresh one — never EventSource auto-reconnect.
    await vi.advanceTimersByTimeAsync(1000);
    expect(createStreamTicket).toHaveBeenCalledTimes(2);
    const es2 = FakeEventSource.instances[1];
    expect(es2.url).not.toBe(FakeEventSource.instances[0].url);

    // Reconnect open IS the resume story: bulk invalidate active queries.
    es2.emitOpen();
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(2);
    stream.releaseLiveStream();
  });

  it('watchdog force-closes a silent connection and re-enters the mint loop', async () => {
    const { createStreamTicket, stream, status } = await loadLive();
    stream.acquireLiveStream();
    await flush();
    const es = FakeEventSource.instances[0];
    es.emitOpen();

    // Any frame resets the watchdog: 60s silence, then a sys.ping, then
    // another 60s — still under the 75s budget from the last frame.
    await vi.advanceTimersByTimeAsync(60_000);
    es.emitFrame({ type: 'sys.ping', time: '2026-07-15T00:01:00Z' });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(es.closed).toBe(false);
    expect(status.liveEventsStatus()).toBe('open');

    // 75s after the last frame (3 missed 25s pings) the connection is
    // half-open: force-close and go through the normal backoff/re-mint loop.
    await vi.advanceTimersByTimeAsync(15_000);
    expect(es.closed).toBe(true);
    expect(status.liveEventsStatus()).toBe('closed');

    await vi.advanceTimersByTimeAsync(1000);
    expect(createStreamTicket).toHaveBeenCalledTimes(2);
    expect(status.liveEventsStatus()).toBe('connecting');
    stream.releaseLiveStream();
  });

  it('refcounts holds: only the last release closes the source', async () => {
    const { stream, status } = await loadLive();
    stream.acquireLiveStream();
    await flush();
    const es = FakeEventSource.instances[0];
    es.emitOpen();

    stream.acquireLiveStream(); // second hold shares the connection
    expect(FakeEventSource.instances).toHaveLength(1);

    stream.releaseLiveStream();
    expect(es.closed).toBe(false);
    expect(status.liveEventsStatus()).toBe('open');

    stream.releaseLiveStream();
    expect(es.closed).toBe(true);
    expect(status.liveEventsStatus()).toBe('closed');
  });

  it('liveFallback returns false only while the stream is open', async () => {
    const { stream, status } = await loadLive();
    const interval = status.liveFallback(30_000);
    expect(interval()).toBe(30_000); // idle

    stream.acquireLiveStream();
    await flush();
    FakeEventSource.instances[0].emitOpen();
    expect(interval()).toBe(false);

    FakeEventSource.instances[0].onerror?.();
    expect(interval()).toBe(30_000); // closed → fall back to base polling
    stream.releaseLiveStream();
  });
});
