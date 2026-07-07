import { renderHook, waitFor, act } from '@testing-library/react';
import React from 'react';
import { TextEncoder, TextDecoder } from 'util';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';

// jsdom omits TextEncoder/TextDecoder; the proxy transport decodes stream
// chunks with them, so polyfill from node's util before importing the hook.
const g = globalThis as unknown as { TextEncoder?: unknown; TextDecoder?: unknown };
if (!g.TextEncoder) g.TextEncoder = TextEncoder;
if (!g.TextDecoder) g.TextDecoder = TextDecoder;

jest.mock('@/lib/api', () => ({
  createStreamTicket: jest.fn().mockResolvedValue({ ticket: 'tkt' }),
}));

import {
  useResourceWatch,
  useResourceWatchInvalidation,
  foldWatchFrame,
  identityOf,
  type K8sList,
} from './use-resource-watch';

// ---- Fakes ---------------------------------------------------------------

// Minimal EventSource that records verb listeners and lets a test push frames.
class FakeEventSource {
  static instances: FakeEventSource[] = [];
  url: string;
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  listeners: Record<string, ((ev: MessageEvent) => void)[]> = {};
  closed = false;
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

function makeWrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: React.ReactNode }) =>
    React.createElement(QueryClientProvider, { client }, children);
  return { client, wrapper };
}

type Pod = { metadata?: { uid?: string; name?: string; namespace?: string }; phase?: string };

// ---- Pure reducer --------------------------------------------------------

describe('foldWatchFrame', () => {
  const a: Pod = { metadata: { uid: 'a', name: 'a', namespace: 'ns' } };
  const b: Pod = { metadata: { uid: 'b', name: 'b', namespace: 'ns' } };

  it('ADDED appends, MODIFIED replaces in place, DELETED removes', () => {
    let list: K8sList<Pod> | undefined = undefined;
    list = foldWatchFrame(list, 'ADDED', a);
    list = foldWatchFrame(list, 'ADDED', b);
    expect(list.items?.map((i) => i.metadata?.uid)).toEqual(['a', 'b']);

    list = foldWatchFrame(list, 'MODIFIED', { metadata: { uid: 'a' }, phase: 'Running' });
    expect(list.items?.find((i) => i.metadata?.uid === 'a')?.phase).toBe('Running');
    expect(list.items?.length).toBe(2);

    list = foldWatchFrame(list, 'DELETED', a);
    expect(list.items?.map((i) => i.metadata?.uid)).toEqual(['b']);
  });

  it('falls back to namespace/name identity when uid is absent', () => {
    expect(identityOf({ metadata: { name: 'x', namespace: 'ns' } })).toBe('ns/x');
  });

  it('ignores frames with no object', () => {
    const prev: K8sList<Pod> = { items: [a] };
    expect(foldWatchFrame(prev, 'ADDED', undefined)).toBe(prev);
  });
});

// ---- SSE transport (pods) ------------------------------------------------

describe('useResourceWatch — pods SSE transport', () => {
  const key = ['clusters', 'c1', 'pods'];

  beforeEach(() => {
    FakeEventSource.instances = [];
    (globalThis as unknown as { EventSource: unknown }).EventSource = FakeEventSource;
  });

  it('folds ADDED/MODIFIED/DELETED SSE frames into the query cache', async () => {
    const { client, wrapper } = makeWrapper();
    const { result } = renderHook(
      () =>
        useResourceWatch<Pod>({
          clusterId: 'c1',
          queryKey: key,
          source: { kind: 'pods', namespace: 'ns' },
        }),
      { wrapper },
    );

    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
    const es = FakeEventSource.instances[0];
    expect(es.url).toContain('/clusters/c1/pods/watch/');
    expect(es.url).toContain('namespace=ns');
    expect(es.url).toContain('ticket=tkt');

    act(() => es.onopen?.());
    expect(result.current.live).toBe(true);

    act(() => es.emit('ADDED', { metadata: { uid: 'p1', name: 'p1', namespace: 'ns' }, phase: 'Pending' }));
    act(() => es.emit('ADDED', { metadata: { uid: 'p2', name: 'p2', namespace: 'ns' } }));
    expect(client.getQueryData<K8sList<Pod>>(key)?.items?.map((i) => i.metadata?.uid)).toEqual(['p1', 'p2']);

    act(() => es.emit('MODIFIED', { metadata: { uid: 'p1', name: 'p1', namespace: 'ns' }, phase: 'Running' }));
    expect(
      client.getQueryData<K8sList<Pod>>(key)?.items?.find((i) => i.metadata?.uid === 'p1')?.phase,
    ).toBe('Running');

    act(() => es.emit('DELETED', { metadata: { uid: 'p2', name: 'p2', namespace: 'ns' } }));
    expect(client.getQueryData<K8sList<Pod>>(key)?.items?.map((i) => i.metadata?.uid)).toEqual(['p1']);
  });

  it('reports fallback when the stream errors', async () => {
    const { wrapper } = makeWrapper();
    const { result } = renderHook(
      () => useResourceWatch<Pod>({ clusterId: 'c1', queryKey: key, source: { kind: 'pods' } }),
      { wrapper },
    );
    await waitFor(() => expect(FakeEventSource.instances.length).toBeGreaterThan(0));
    act(() => FakeEventSource.instances[0].onerror?.());
    await waitFor(() => expect(result.current.status).toBe('fallback'));
    expect(result.current.live).toBe(false);
  });
});

// ---- Proxy transport (generic k8s ?watch=true NDJSON) --------------------

describe('useResourceWatch — proxy fetch-stream transport', () => {
  const key = ['clusters', 'c1', 'workloads', 'deployments'];

  function streamResponse(lines: string[]): Response {
    const encoder = new TextEncoder();
    let i = 0;
    const body = {
      getReader() {
        return {
          read() {
            if (i < lines.length) {
              const chunk = encoder.encode(lines[i] + '\n');
              i += 1;
              return Promise.resolve({ value: chunk, done: false });
            }
            return Promise.resolve({ value: undefined, done: true });
          },
        };
      },
    };
    return { ok: true, body } as unknown as Response;
  }

  it('folds NDJSON watch frames from the /k8s/ proxy into the cache', async () => {
    const { client, wrapper } = makeWrapper();
    global.fetch = jest.fn().mockResolvedValue(
      streamResponse([
        JSON.stringify({ type: 'ADDED', object: { metadata: { uid: 'd1', name: 'd1', namespace: 'ns' } } }),
        JSON.stringify({ type: 'ADDED', object: { metadata: { uid: 'd2', name: 'd2', namespace: 'ns' } } }),
        JSON.stringify({ type: 'DELETED', object: { metadata: { uid: 'd1', name: 'd1', namespace: 'ns' } } }),
      ]),
    ) as unknown as typeof fetch;

    renderHook(
      () =>
        useResourceWatch({
          clusterId: 'c1',
          queryKey: key,
          source: { kind: 'proxy', path: 'apis/apps/v1/deployments' },
        }),
      { wrapper },
    );

    await waitFor(() =>
      expect(client.getQueryData<K8sList>(key)?.items?.map((i) => i.metadata?.uid)).toEqual(['d2']),
    );
    const calledUrl = (global.fetch as jest.Mock).mock.calls[0][0] as string;
    expect(calledUrl).toContain('/clusters/c1/k8s/apis/apps/v1/deployments?watch=true');
  });

  it('reports fallback when the proxy watch cannot open', async () => {
    const { wrapper } = makeWrapper();
    global.fetch = jest.fn().mockResolvedValue({ ok: false, body: null } as unknown as Response) as unknown as typeof fetch;
    const { result } = renderHook(
      () =>
        useResourceWatch({
          clusterId: 'c1',
          queryKey: key,
          source: { kind: 'proxy', path: 'apis/apps/v1/deployments' },
        }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.status).toBe('fallback'));
    expect(result.current.live).toBe(false);
  });

  function streamResponseLocal(lines: string[]): Response {
    const encoder = new TextEncoder();
    let i = 0;
    const body = {
      getReader() {
        return {
          read() {
            if (i < lines.length) {
              const chunk = encoder.encode(lines[i] + '\n');
              i += 1;
              return Promise.resolve({ value: chunk, done: false });
            }
            return Promise.resolve({ value: undefined, done: true });
          },
        };
      },
    };
    return { ok: true, body } as unknown as Response;
  }

  it('useResourceWatchInvalidation invalidates the list query on a change frame', async () => {
    const { client, wrapper } = makeWrapper();
    const listKey = ['generic', 'c1', 'configmaps'];
    const invPrefix = ['generic', 'c1'];
    // Seed a cached list so there is something to invalidate.
    client.setQueryData(listKey, [{ name: 'cm1' }]);
    const invalidateSpy = jest.spyOn(client, 'invalidateQueries');

    global.fetch = jest.fn().mockResolvedValue(
      streamResponseLocal([
        JSON.stringify({ type: 'MODIFIED', object: { metadata: { uid: 'cm1', name: 'cm1', namespace: 'ns' } } }),
      ]),
    ) as unknown as typeof fetch;

    renderHook(
      () =>
        useResourceWatchInvalidation({
          clusterId: 'c1',
          path: 'api/v1/configmaps',
          queryKeys: [invPrefix],
          debounceMs: 10,
        }),
      { wrapper },
    );

    await waitFor(() => expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: invPrefix }));
    const calledUrl = (global.fetch as jest.Mock).mock.calls[0][0] as string;
    expect(calledUrl).toContain('/clusters/c1/k8s/api/v1/configmaps?watch=true');
  });
});
