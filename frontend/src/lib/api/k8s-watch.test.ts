// Transport tests for the generic NDJSON proxy watch (ported from the
// pre-migration resource-watch transport suite): request shape, chunk
// buffering, malformed-frame tolerance, fallback reporting, and abort.
import { openProxyWatch, type WatchVerb } from './k8s-watch';

vi.mock('../api', () => ({
  createStreamTicket: vi.fn(),
}));

function streamResponse(chunks: string[]): Response {
  const encoder = new TextEncoder();
  let index = 0;
  const body = {
    getReader() {
      return {
        read() {
          if (index < chunks.length) {
            return Promise.resolve({ value: encoder.encode(chunks[index++]), done: false });
          }
          return Promise.resolve({ value: undefined, done: true });
        },
      };
    },
  };
  return { ok: true, body } as unknown as Response;
}

async function waitForCondition(predicate: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error('condition was not met');
}

describe('openProxyWatch', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it('builds an authenticated watch request and emits valid frames', async () => {
    const onFrame = vi.fn<(verb: WatchVerb, obj: unknown) => void>();
    const onStatus = vi.fn();
    global.fetch = vi.fn().mockResolvedValue(
      streamResponse([
        `${JSON.stringify({ type: 'ADDED', object: { metadata: { uid: 'one' } } })}\n`,
        `${JSON.stringify({ type: 'MODIFIED', object: { metadata: { uid: 'one' } } })}\n`,
      ]),
    ) as unknown as typeof fetch;

    openProxyWatch('cluster-1', 'apis/apps/v1/deployments?namespace=team-a', onFrame, onStatus);

    await waitForCondition(() => onStatus.mock.calls.some(([status]) => status === 'fallback'));
    expect(global.fetch).toHaveBeenCalledWith(
      '/api/v1/clusters/cluster-1/k8s/apis/apps/v1/deployments?namespace=team-a&watch=true',
      expect.objectContaining({
        method: 'GET',
        credentials: 'include',
        headers: { Accept: 'application/json' },
        signal: expect.any(AbortSignal),
      }),
    );
    expect(onStatus.mock.calls.map(([status]) => status)).toEqual(['live', 'fallback']);
    expect(onFrame).toHaveBeenCalledTimes(2);
  });

  it('buffers a frame split across chunks', async () => {
    const onFrame = vi.fn();
    const frame = JSON.stringify({
      type: 'ADDED',
      object: { metadata: { uid: 'split-frame' } },
    });
    global.fetch = vi.fn().mockResolvedValue(
      streamResponse([frame.slice(0, 17), frame.slice(17), '\n']),
    ) as unknown as typeof fetch;

    openProxyWatch('cluster-1', 'api/v1/pods', onFrame, vi.fn());

    await waitForCondition(() => onFrame.mock.calls.length === 1);
    expect(onFrame).toHaveBeenCalledWith(
      'ADDED',
      expect.objectContaining({ metadata: { uid: 'split-frame' } }),
    );
  });

  it('ignores malformed and unsupported frames without losing later frames', async () => {
    const onFrame = vi.fn();
    global.fetch = vi.fn().mockResolvedValue(
      streamResponse([
        'not-json\n',
        `${JSON.stringify({ type: 'BOOKMARK', object: { metadata: { uid: 'ignored' } } })}\n`,
        `${JSON.stringify({ type: 'DELETED', object: { metadata: { uid: 'kept' } } })}\n`,
      ]),
    ) as unknown as typeof fetch;

    openProxyWatch('cluster-1', 'api/v1/configmaps', onFrame, vi.fn());

    await waitForCondition(() => onFrame.mock.calls.length === 1);
    expect(onFrame).toHaveBeenCalledWith(
      'DELETED',
      expect.objectContaining({ metadata: { uid: 'kept' } }),
    );
  });

  it('reports fallback for a non-success response', async () => {
    const onStatus = vi.fn();
    global.fetch = vi
      .fn()
      .mockResolvedValue({ ok: false, status: 503, body: null }) as unknown as typeof fetch;

    openProxyWatch('cluster-1', 'api/v1/pods', vi.fn(), onStatus);

    await waitForCondition(() => onStatus.mock.calls.length === 1);
    expect(onStatus).toHaveBeenCalledWith('fallback');
  });

  it('reports fallback when the request fails', async () => {
    const onStatus = vi.fn();
    global.fetch = vi
      .fn()
      .mockRejectedValue(new TypeError('network unavailable')) as unknown as typeof fetch;

    openProxyWatch('cluster-1', 'api/v1/pods', vi.fn(), onStatus);

    await waitForCondition(() => onStatus.mock.calls.length === 1);
    expect(onStatus).toHaveBeenCalledWith('fallback');
  });

  it('aborts the in-flight request and suppresses fallback after cancellation', async () => {
    const onStatus = vi.fn();
    let requestSignal: AbortSignal | undefined;
    global.fetch = vi.fn((_url: RequestInfo | URL, init?: RequestInit) => {
      requestSignal = init?.signal ?? undefined;
      return new Promise<Response>((_resolve, reject) => {
        requestSignal?.addEventListener('abort', () =>
          reject(new DOMException('aborted', 'AbortError')),
        );
      });
    }) as typeof fetch;

    const stop = openProxyWatch('cluster-1', 'api/v1/pods', vi.fn(), onStatus);
    await waitForCondition(() => requestSignal !== undefined);

    stop();
    expect(requestSignal?.aborted).toBe(true);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(onStatus).not.toHaveBeenCalled();
  });
});
