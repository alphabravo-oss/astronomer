import { TextEncoder, TextDecoder } from 'util';

// jsdom omits TextEncoder/TextDecoder; the proxy transport decodes stream
// chunks with them, so polyfill from node's util before importing the module.
const g = globalThis as unknown as { TextEncoder?: unknown; TextDecoder?: unknown };
if (!g.TextEncoder) g.TextEncoder = TextEncoder;
if (!g.TextDecoder) g.TextDecoder = TextDecoder;

vi.mock('@/lib/api', () => ({
  createStreamTicket: vi.fn().mockResolvedValue({ ticket: 'tkt' }),
  k8sGet: vi.fn(),
}));

import { waitFor } from '@testing-library/react';
import { k8sGet } from '@/lib/api';
import { identityOf, k8sCollection, podRowFromRaw, type RawPod } from './collections';

const k8sGetMock = vi.mocked(k8sGet);

type Obj = { metadata: { uid?: string; name: string; namespace: string }; phase?: string };

const obj = (uid: string, name: string, ns = 'ns', extra?: Partial<Obj>): Obj => ({
  metadata: { uid, name, namespace: ns },
  ...extra,
});

// ---- Controllable NDJSON proxy stream (the `?watch=true` transport) -------

function fakeProxyStream() {
  const encoder = new TextEncoder();
  const queue: Array<{ value?: Uint8Array; done: boolean }> = [];
  let pending: ((r: { value?: Uint8Array; done: boolean }) => void) | null = null;
  const deliver = (chunk: { value?: Uint8Array; done: boolean }) => {
    if (pending) {
      const p = pending;
      pending = null;
      p(chunk);
    } else {
      queue.push(chunk);
    }
  };
  const response = {
    ok: true,
    body: {
      getReader() {
        return {
          read(): Promise<{ value?: Uint8Array; done: boolean }> {
            if (queue.length > 0) return Promise.resolve(queue.shift()!);
            return new Promise((resolve) => {
              pending = resolve;
            });
          },
        };
      },
    },
  } as unknown as Response;
  return {
    response,
    push(verb: string, o: unknown) {
      deliver({ value: encoder.encode(JSON.stringify({ type: verb, object: o }) + '\n'), done: false });
    },
    end() {
      deliver({ done: true });
    },
  };
}

// ---- Minimal EventSource fake (the pods SSE transport) --------------------

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  closed = false;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners: Record<string, ((ev: MessageEvent) => void)[]> = {};
  constructor(url: string) {
    this.url = url;
    FakeEventSource.instances.push(this);
  }
  addEventListener(type: string, cb: (ev: MessageEvent) => void): void {
    (this.listeners[type] ??= []).push(cb);
  }
  close(): void {
    this.closed = true;
  }
  emit(type: string, data: unknown): void {
    const ev = { data: JSON.stringify(data) } as MessageEvent;
    (this.listeners[type] ?? []).forEach((cb) => cb(ev));
  }
}

const uids = (c: { toArray: Obj[] }) => c.toArray.map((i) => i.metadata.uid);

describe('identityOf', () => {
  it('prefers uid, falls back to namespace/name', () => {
    expect(identityOf({ metadata: { uid: 'u1', name: 'x', namespace: 'ns' } })).toBe('u1');
    expect(identityOf({ metadata: { name: 'x', namespace: 'ns' } })).toBe('ns/x');
  });
});

describe('k8sCollection — proxy NDJSON source', () => {
  beforeEach(() => {
    k8sGetMock.mockReset();
  });

  it('caches handles per (cluster, source)', () => {
    const a = k8sCollection({ clusterId: 'cache', source: { kind: 'proxy', path: 'apis/apps/v1/deployments' } });
    const b = k8sCollection({ clusterId: 'cache', source: { kind: 'proxy', path: 'apis/apps/v1/deployments' } });
    const c = k8sCollection({ clusterId: 'cache', source: { kind: 'proxy', path: 'apis/apps/v1/daemonsets' } });
    expect(a).toBe(b);
    expect(a).not.toBe(c);
  });

  it('seeds from the REST list then folds ADDED/MODIFIED/DELETED frames in order', async () => {
    const stream = fakeProxyStream();
    k8sGetMock.mockResolvedValue({ items: [obj('a', 'a')] });
    global.fetch = vi.fn().mockResolvedValue(stream.response) as unknown as typeof fetch;

    const { collection, status } = k8sCollection<Obj>({
      clusterId: 'c-fold',
      source: { kind: 'proxy', path: 'apis/apps/v1/deployments' },
    });
    await collection.preload();
    expect(k8sGetMock).toHaveBeenCalledWith('c-fold', 'apis/apps/v1/deployments');
    expect(uids(collection)).toEqual(['a']);
    await waitFor(() => expect(status.state).toBe('live'));
    const calledUrl = (global.fetch as ReturnType<typeof vi.fn>).mock.calls[0][0] as string;
    expect(calledUrl).toContain('/clusters/c-fold/k8s/apis/apps/v1/deployments?watch=true');

    // ADDED appends (and replays of the seeded object upsert, not duplicate).
    stream.push('ADDED', obj('a', 'a'));
    stream.push('ADDED', obj('b', 'b'));
    await waitFor(() => expect(uids(collection)).toEqual(['a', 'b']));

    // MODIFIED replaces in place.
    stream.push('MODIFIED', obj('a', 'a', 'ns', { phase: 'Running' }));
    await waitFor(() => expect(collection.get('a')?.phase).toBe('Running'));
    expect(collection.size).toBe(2);

    // DELETED removes (and a repeat delete is a no-op).
    stream.push('DELETED', obj('b', 'b'));
    await waitFor(() => expect(uids(collection)).toEqual(['a']));
    stream.push('DELETED', obj('b', 'b'));
    stream.push('ADDED', obj('c', 'c'));
    await waitFor(() => expect(uids(collection)).toEqual(['a', 'c']));
  });

  it('marks fallback on stream drop and re-seeds on the backoff retry', async () => {
    vi.useFakeTimers();
    try {
      const first = fakeProxyStream();
      const second = fakeProxyStream();
      k8sGetMock
        .mockResolvedValueOnce({ items: [obj('a', 'a')] })
        .mockResolvedValueOnce({ items: [obj('b', 'b')] });
      global.fetch = vi
        .fn()
        .mockResolvedValueOnce(first.response)
        .mockResolvedValueOnce(second.response) as unknown as typeof fetch;

      const { collection, status } = k8sCollection<Obj>({
        clusterId: 'c-retry',
        source: { kind: 'proxy', path: 'apis/apps/v1/deployments' },
      });
      const ready = collection.preload();
      await vi.waitFor(() => expect(collection.size).toBe(1));
      await ready;

      // Server ends the watch → fallback + scheduled retry.
      first.end();
      await vi.waitFor(() => expect(status.state).toBe('fallback'));

      await vi.advanceTimersByTimeAsync(1_000);
      // Retry re-seeds: truncate + fresh list means 'a' (deleted while the
      // stream was down) is gone and 'b' is present.
      await vi.waitFor(() => expect(uids(collection)).toEqual(['b']));
      expect(k8sGetMock).toHaveBeenCalledTimes(2);
    } finally {
      vi.useRealTimers();
    }
  });

  it('marks error (but ready and empty) when the seed list fails', async () => {
    vi.useFakeTimers();
    try {
      k8sGetMock.mockRejectedValue(new Error('proxy 503'));
      global.fetch = vi.fn() as unknown as typeof fetch;
      const { collection, status } = k8sCollection<Obj>({
        clusterId: 'c-seed-fail',
        source: { kind: 'proxy', path: 'apis/apps/v1/deployments' },
      });
      await collection.preload();
      expect(collection.size).toBe(0);
      expect(status.state).toBe('error');
      expect(global.fetch).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe('k8sCollection — pods SSE source', () => {
  beforeEach(() => {
    k8sGetMock.mockReset();
    FakeEventSource.instances = [];
    (globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource;
  });

  it('seeds the (cluster, namespace) list and folds SSE frames', async () => {
    k8sGetMock.mockResolvedValue({ items: [] });
    const { collection, status } = k8sCollection<Obj>({
      clusterId: 'c-pods',
      source: { kind: 'pods', namespace: 'ns1' },
    });
    await collection.preload();
    expect(k8sGetMock).toHaveBeenCalledWith('c-pods', 'api/v1/namespaces/ns1/pods');

    await waitFor(() => expect(FakeEventSource.instances.length).toBe(1));
    const es = FakeEventSource.instances[0];
    expect(es.url).toContain('/clusters/c-pods/pods/watch/');
    expect(es.url).toContain('namespace=ns1');
    expect(es.url).toContain('ticket=tkt');

    es.onopen?.();
    expect(status.state).toBe('live');

    es.emit('ADDED', obj('p1', 'p1', 'ns1', { phase: 'Pending' }));
    es.emit('MODIFIED', obj('p1', 'p1', 'ns1', { phase: 'Running' }));
    es.emit('ADDED', obj('p2', 'p2', 'ns1'));
    es.emit('DELETED', obj('p2', 'p2', 'ns1'));
    await waitFor(() => expect(uids(collection)).toEqual(['p1']));
    expect(collection.get('p1')?.phase).toBe('Running');
  });
});

describe('podRowFromRaw', () => {
  it('shapes a raw pod into the display row (ready/restarts/status per container)', () => {
    const raw: RawPod = {
      metadata: {
        name: 'web-1',
        namespace: 'prod',
        uid: 'u1',
        creationTimestamp: new Date(Date.now() - 5 * 60_000).toISOString(),
      },
      spec: {
        nodeName: 'node-1',
        containers: [
          { name: 'app', image: 'nginx:1.25', ports: [{ containerPort: 80, protocol: 'TCP' }] },
          { name: 'sidecar', image: 'busybox' },
        ],
      },
      status: {
        phase: 'Running',
        podIP: '10.0.0.9',
        containerStatuses: [
          { name: 'app', ready: true, restartCount: 2, state: { running: {} } },
          { name: 'sidecar', ready: false, restartCount: 1, state: { terminated: {} } },
        ],
        conditions: [{ type: 'Ready', status: 'True', lastTransitionTime: '2026-01-01T00:00:00Z' }],
      },
    };
    const row = podRowFromRaw('c1', raw);
    expect(row).toMatchObject({
      name: 'web-1',
      namespace: 'prod',
      clusterId: 'c1',
      phase: 'Running',
      status: 'Running',
      ready: '1/2',
      restarts: 3,
      node: 'node-1',
      ip: '10.0.0.9',
      age: '5m',
    });
    expect(row.containers).toEqual([
      expect.objectContaining({ name: 'app', status: 'running', ready: true, restartCount: 2 }),
      expect.objectContaining({ name: 'sidecar', status: 'terminated', ready: false, restartCount: 1 }),
    ]);
    expect(row.conditions[0]).toMatchObject({ type: 'Ready', status: 'True', lastTransition: '2026-01-01T00:00:00Z' });
  });

  it('defaults sanely when status is missing', () => {
    const row = podRowFromRaw('c1', {
      metadata: { name: 'p', namespace: 'ns' },
      spec: { containers: [{ name: 'app', image: 'img' }] },
    });
    expect(row.phase).toBe('Unknown');
    expect(row.ready).toBe('0/1');
    expect(row.restarts).toBe(0);
    expect(row.containers[0].status).toBe('waiting');
    expect(row.age).toBe('');
  });
});
