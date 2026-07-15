import type { QueryClient } from '@tanstack/react-query';
import { clearPacedInvalidations, pacedInvalidate } from './paced-invalidate';

function fakeQueryClient() {
  return { invalidateQueries: vi.fn() } as unknown as QueryClient & {
    invalidateQueries: ReturnType<typeof vi.fn>;
  };
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  // The throttler map is module-level state — drop it between tests.
  clearPacedInvalidations();
  vi.useRealTimers();
});

describe('pacedInvalidate', () => {
  it('coalesces a burst into exactly one leading + one trailing invalidate', () => {
    const qc = fakeQueryClient();
    const key = ['clusters', 'list'];

    for (let i = 0; i < 10; i += 1) {
      pacedInvalidate(qc, key);
    }
    // Leading: the first event of the burst invalidates instantly.
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);
    expect(qc.invalidateQueries).toHaveBeenCalledWith({ queryKey: key });

    // Trailing: the remaining 9 collapse into one invalidate at window end.
    vi.advanceTimersByTime(400);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(2);
    expect(qc.invalidateQueries).toHaveBeenLastCalledWith({ queryKey: key });

    // Nothing else pending.
    vi.advanceTimersByTime(2000);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(2);
  });

  it('a single event invalidates exactly once (leading, no trailing echo)', () => {
    const qc = fakeQueryClient();
    pacedInvalidate(qc, ['activity']);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(2000);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);
  });

  it('throttles per stringified key — distinct keys are independent', () => {
    const qc = fakeQueryClient();
    const a = ['clusters', 'c-1', 'pods'];
    const b = ['clusters', 'c-2', 'pods'];

    pacedInvalidate(qc, a);
    pacedInvalidate(qc, b);
    // Both leading executions fire — different keys, different throttlers.
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(2);
    expect(qc.invalidateQueries).toHaveBeenNthCalledWith(1, { queryKey: a });
    expect(qc.invalidateQueries).toHaveBeenNthCalledWith(2, { queryKey: b });
  });

  it('clearPacedInvalidations cancels pending trailing invalidations', () => {
    const qc = fakeQueryClient();
    const key = ['workloads', 'c-1'];

    pacedInvalidate(qc, key);
    pacedInvalidate(qc, key); // queues a trailing invalidate
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);

    clearPacedInvalidations(); // stream closed — drop the pending trailer
    vi.advanceTimersByTime(2000);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(1);

    // A fresh event after the clear gets a fresh leading execution.
    pacedInvalidate(qc, key);
    expect(qc.invalidateQueries).toHaveBeenCalledTimes(2);
  });
});
