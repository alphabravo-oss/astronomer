import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';

vi.mock('@/lib/api', () => ({
  createStreamTicket: vi.fn().mockResolvedValue({ ticket: 'tkt' }),
}));

import { useLiveEvents, useLiveQueryInvalidation } from '@/lib/live/hooks';
import type { LiveEvent } from '@/lib/live/envelope';

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  close(): void {
    /* no-op */
  }
  emitFrame(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

function makeWrapper(queryClient: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  FakeEventSource.instances = [];
  (globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource;
});

describe('live hooks over the dispatcher-fed stream target', () => {
  it('subscribe() receives registration events dispatched via onmessage', async () => {
    // Port of the old KNOWN_EVENT_TYPES regression test: registration events
    // are NOT the default browser message type, but with envelope-type
    // dispatch they need no per-type addEventListener registration.
    const queryClient = new QueryClient();
    const { result, unmount } = renderHook(() => useLiveEvents(), {
      wrapper: makeWrapper(queryClient),
    });
    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
    const es = FakeEventSource.instances[FakeEventSource.instances.length - 1];
    es.onopen?.();

    const seen: LiveEvent[] = [];
    const off = result.current.subscribe(
      ['cluster.registration.step', 'cluster.registration.phase'],
      (ev) => seen.push(ev),
    );
    es.emitFrame({ id: 1, type: 'cluster.registration.step', time: 't', data: { cluster_id: 'c1' } });
    es.emitFrame({ id: 2, type: 'cluster.registration.phase', time: 't', data: { cluster_id: 'c1' } });

    expect(seen.map((e) => e.type)).toEqual([
      'cluster.registration.step',
      'cluster.registration.phase',
    ]);
    // Central camelization: subscribers see camelCase payloads.
    expect(seen[0].data).toEqual({ clusterId: 'c1' });
    off();
    unmount();
  });

  it('useLiveQueryInvalidation invalidates the given keys when a matching event fires', async () => {
    const queryClient = new QueryClient();
    const spy = vi.spyOn(queryClient, 'invalidateQueries');
    // Arbitrary caller-shaped keys (not factory entries — the hook forwards
    // whatever prefix it is given).
    const clustersKey = ['clusters'];
    const agentsKey = ['agents'];
    const { unmount } = renderHook(
      () => useLiveQueryInvalidation('cluster.heartbeat', [clustersKey, agentsKey]),
      { wrapper: makeWrapper(queryClient) },
    );
    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
    const es = FakeEventSource.instances[FakeEventSource.instances.length - 1];
    es.onopen?.();
    spy.mockClear(); // ignore any connect-transition invalidations

    es.emitFrame({ id: 3, type: 'cluster.heartbeat', time: 't', data: { cluster_id: 'c1' } });
    expect(spy).toHaveBeenCalledWith({ queryKey: clustersKey });
    expect(spy).toHaveBeenCalledWith({ queryKey: agentsKey });

    spy.mockClear();
    es.emitFrame({ id: 4, type: 'cluster.metrics', time: 't', data: { cluster_id: 'c1' } });
    // Non-subscribed type: the hook's own keys stay untouched. (The central
    // dispatcher still routes the tick to its precise metrics prefix —
    // that's EVENT_ROUTES' job, not this hook's.)
    expect(spy).not.toHaveBeenCalledWith({ queryKey: clustersKey });
    expect(spy).not.toHaveBeenCalledWith({ queryKey: agentsKey });
    unmount();
  });
});
