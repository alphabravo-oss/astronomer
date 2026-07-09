import { TextDecoder, TextEncoder } from 'util';
import { consumeResourceWatch, type ResourceWatchVerb } from './resource-watch';

const globals = globalThis as unknown as {
  TextDecoder?: typeof TextDecoder;
  TextEncoder?: typeof TextEncoder;
};
if (!globals.TextDecoder) globals.TextDecoder = TextDecoder;
if (!globals.TextEncoder) globals.TextEncoder = TextEncoder;

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

describe('consumeResourceWatch', () => {
  const originalFetch = global.fetch;

  afterEach(() => {
    global.fetch = originalFetch;
    jest.restoreAllMocks();
  });

  it('builds an authenticated watch request and emits valid frames', async () => {
    const onFrame = jest.fn<void, [ResourceWatchVerb, unknown]>();
    const onStatus = jest.fn();
    global.fetch = jest.fn().mockResolvedValue(
      streamResponse([
        `${JSON.stringify({ type: 'ADDED', object: { metadata: { uid: 'one' } } })}\n`,
        `${JSON.stringify({ type: 'MODIFIED', object: { metadata: { uid: 'one' } } })}\n`,
      ]),
    ) as unknown as typeof fetch;

    consumeResourceWatch({
      clusterId: 'cluster-1',
      path: 'apis/apps/v1/deployments?namespace=team-a',
      onFrame,
      onStatus,
    });

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
    const onFrame = jest.fn();
    const frame = JSON.stringify({
      type: 'ADDED',
      object: { metadata: { uid: 'split-frame' } },
    });
    global.fetch = jest.fn().mockResolvedValue(
      streamResponse([frame.slice(0, 17), frame.slice(17), '\n']),
    ) as unknown as typeof fetch;

    consumeResourceWatch({
      clusterId: 'cluster-1',
      path: 'api/v1/pods',
      onFrame,
      onStatus: jest.fn(),
    });

    await waitForCondition(() => onFrame.mock.calls.length === 1);
    expect(onFrame).toHaveBeenCalledWith(
      'ADDED',
      expect.objectContaining({ metadata: { uid: 'split-frame' } }),
    );
  });

  it('ignores malformed and unsupported frames without losing later frames', async () => {
    const onFrame = jest.fn();
    global.fetch = jest.fn().mockResolvedValue(
      streamResponse([
        'not-json\n',
        `${JSON.stringify({ type: 'BOOKMARK', object: { metadata: { uid: 'ignored' } } })}\n`,
        `${JSON.stringify({ type: 'DELETED', object: { metadata: { uid: 'kept' } } })}\n`,
      ]),
    ) as unknown as typeof fetch;

    consumeResourceWatch({
      clusterId: 'cluster-1',
      path: 'api/v1/configmaps',
      onFrame,
      onStatus: jest.fn(),
    });

    await waitForCondition(() => onFrame.mock.calls.length === 1);
    expect(onFrame).toHaveBeenCalledWith(
      'DELETED',
      expect.objectContaining({ metadata: { uid: 'kept' } }),
    );
  });

  it('reports fallback for a non-success response', async () => {
    const onStatus = jest.fn();
    global.fetch = jest.fn().mockResolvedValue({ ok: false, status: 503, body: null }) as unknown as typeof fetch;

    consumeResourceWatch({
      clusterId: 'cluster-1',
      path: 'api/v1/pods',
      onFrame: jest.fn(),
      onStatus,
    });

    await waitForCondition(() => onStatus.mock.calls.length === 1);
    expect(onStatus).toHaveBeenCalledWith('fallback');
  });

  it('reports fallback when the request fails', async () => {
    const onStatus = jest.fn();
    global.fetch = jest.fn().mockRejectedValue(new TypeError('network unavailable')) as unknown as typeof fetch;

    consumeResourceWatch({
      clusterId: 'cluster-1',
      path: 'api/v1/pods',
      onFrame: jest.fn(),
      onStatus,
    });

    await waitForCondition(() => onStatus.mock.calls.length === 1);
    expect(onStatus).toHaveBeenCalledWith('fallback');
  });

  it('aborts the in-flight request and suppresses fallback after cancellation', async () => {
    const onStatus = jest.fn();
    let requestSignal: AbortSignal | undefined;
    global.fetch = jest.fn((_url: RequestInfo | URL, init?: RequestInit) => {
      requestSignal = init?.signal ?? undefined;
      return new Promise<Response>((_resolve, reject) => {
        requestSignal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
    }) as typeof fetch;

    const stop = consumeResourceWatch({
      clusterId: 'cluster-1',
      path: 'api/v1/pods',
      onFrame: jest.fn(),
      onStatus,
    });
    await waitForCondition(() => requestSignal !== undefined);

    stop();
    expect(requestSignal?.aborted).toBe(true);
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(onStatus).not.toHaveBeenCalled();
  });
});
