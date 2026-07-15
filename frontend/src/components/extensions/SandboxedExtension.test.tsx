// §BridgeProtocol — SandboxedExtension host-component tests.
//
// We drive the bridge by stubbing the iframe's contentWindow.postMessage and
// dispatching window 'message' events as if the iframe sent them, asserting:
//   - the host speaks first with host/hello on iframe load (handshake + allowlist),
//   - a spoofed message (wrong origin / wrong source) is IGNORED (no broker call),
//   - ext/data.request for an allowed dataSource brokers §DataProxy and replies
//     host/data.response correlated by id,
//   - ext/data.request for a dataSource outside the handshake allowlist is denied
//     without ever calling the proxy.

import type { Mock, MockedFunction } from 'vitest';
import { render, act, waitFor } from '@testing-library/react';
import { SandboxedExtension, cspToString } from './SandboxedExtension';
import * as extensionsApi from '@/lib/api/extensions';
import type { ExtensionMount, ExtensionDataResponse } from '@/lib/api/extensions';

vi.mock('@/lib/navigation', () => ({
  __esModule: true,
  useRouter: () => ({ push: vi.fn() }),
}));

vi.mock('@/lib/api/extensions', () => ({
  __esModule: true,
  fetchExtensionData: vi.fn(),
  requestExtensionBridgeToken: vi.fn(),
}));

// ExtensionProvider's useExtensionTheme is consumed by the component; the real
// provider degrades to "no theme" outside a provider, which is fine here.
const mockedFetch = extensionsApi.fetchExtensionData as MockedFunction<
  typeof extensionsApi.fetchExtensionData
>;

const ORIGIN = 'https://ext-cost.sandbox.astronomer.local';
const EXT = 'cost-insights';
const POINT_ID = 'ClusterCostTab';

function mount(): ExtensionMount {
  return {
    extension: EXT,
    displayName: 'Cost Insights',
    point: 'clusterTab',
    pointId: POINT_ID,
    tier: 2,
    render: {
      bundle: {
        url: 'https://cdn.example/bundle.js',
        sha256: 'sha256:' + 'a'.repeat(64),
        integrity: 'sha384-xxx',
        entry: 'index.html',
        sandboxOrigin: ORIGIN,
        component: POINT_ID,
        csp: { scriptSrc: ["'self'"], connectSrc: ["'self'"], frameSrc: ["'none'"] },
        dataSources: [{ id: 'podCost', shape: 'list' }],
      },
    },
    dataSources: [{ id: 'podCost', shape: 'list' }],
  };
}

// Replace the iframe's contentWindow with a stub we can both spy on (host->iframe
// posts) and use as the trusted event.source for inbound messages.
function stubIframeWindow(): { post: Mock; win: unknown } {
  const post = vi.fn();
  const iframe = document.querySelector('iframe') as HTMLIFrameElement;
  const win = { postMessage: post };
  Object.defineProperty(iframe, 'contentWindow', { value: win, configurable: true });
  return { post, win };
}

// Fire iframe onLoad (jsdom does not load the cross-origin src itself).
function fireLoad() {
  const iframe = document.querySelector('iframe') as HTMLIFrameElement;
  act(() => {
    iframe.dispatchEvent(new Event('load'));
  });
}

// Dispatch a window 'message' as if it came from `source` at `origin`.
function dispatchMessage(data: unknown, origin: string, source: unknown) {
  act(() => {
    const ev = new MessageEvent('message', { data, origin });
    Object.defineProperty(ev, 'source', { value: source });
    window.dispatchEvent(ev);
  });
}

function bridge(type: string, extra: Record<string, unknown> = {}) {
  return { astronomerBridge: true, v: 1, ext: EXT, mount: POINT_ID, type, ...extra };
}

afterEach(() => vi.clearAllMocks());

describe('SandboxedExtension — iframe + handshake', () => {
  it('renders a sandboxed iframe with the right sandbox/referrer attrs and origin src', () => {
    render(<SandboxedExtension mount={mount()} context={{ clusterId: 'c1' }} manifestSha="sha256:abc" />);
    const iframe = document.querySelector('iframe') as HTMLIFrameElement;
    expect(iframe.getAttribute('sandbox')).toBe('allow-scripts');
    expect(iframe.getAttribute('referrerpolicy')).toBe('no-referrer');
    expect(iframe.getAttribute('allow')).toBe('');
    expect(iframe.getAttribute('src')).toBe(`${ORIGIN}/index.html`);
  });

  it('speaks first with host/hello on load, carrying the dataSource allowlist as ids', () => {
    render(<SandboxedExtension mount={mount()} context={{ clusterId: 'c1' }} manifestSha="sha256:abc" />);
    const { post } = stubIframeWindow();
    fireLoad();
    expect(post).toHaveBeenCalledTimes(1);
    const [msg, targetOrigin] = post.mock.calls[0];
    // host targets the EXACT sandboxOrigin, never "*".
    expect(targetOrigin).toBe(ORIGIN);
    expect(msg.type).toBe('host/hello');
    expect(msg.payload.dataSources).toEqual(['podCost']);
    expect(msg.payload.mount.context).toEqual({ clusterId: 'c1' });
  });
});

describe('SandboxedExtension — inbound origin checks', () => {
  it('ignores a message from a foreign origin (spoofing) — no proxy call, no reply', async () => {
    render(<SandboxedExtension mount={mount()} manifestSha="sha256:abc" />);
    const { post, win } = stubIframeWindow();
    fireLoad();
    post.mockClear(); // drop the host/hello

    // Same window, but a hostile origin -> must be dropped.
    dispatchMessage(bridge('ext/data.request', { id: 'r2', payload: { dataSource: 'podCost' } }), 'https://evil.example', win);

    await Promise.resolve();
    expect(mockedFetch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
  });

  it('ignores a message from a different window even at the right origin', async () => {
    render(<SandboxedExtension mount={mount()} manifestSha="sha256:abc" />);
    const { post } = stubIframeWindow();
    fireLoad();
    post.mockClear();

    dispatchMessage(
      bridge('ext/data.request', { id: 'r2', payload: { dataSource: 'podCost' } }),
      ORIGIN,
      { other: true },
    );

    await Promise.resolve();
    expect(mockedFetch).not.toHaveBeenCalled();
    expect(post).not.toHaveBeenCalled();
  });
});

describe('SandboxedExtension — data request/response brokering', () => {
  it('brokers an allowed ext/data.request through the proxy and replies correlated by id', async () => {
    const res: ExtensionDataResponse = { data: { rows: [{ ns: 'a' }] }, shape: 'list', meta: { dataSourceId: 'podCost' } };
    mockedFetch.mockResolvedValue(res);

    render(<SandboxedExtension mount={mount()} context={{ clusterId: 'c1' }} manifestSha="sha256:abc" />);
    const { post, win } = stubIframeWindow();
    fireLoad();
    post.mockClear();

    dispatchMessage(
      bridge('ext/data.request', { id: 'r2', payload: { dataSource: 'podCost', query: { window: '7d' } } }),
      ORIGIN,
      win,
    );

    await waitFor(() => expect(mockedFetch).toHaveBeenCalled());
    expect(mockedFetch).toHaveBeenCalledWith(EXT, 'podCost', {
      context: { clusterId: 'c1' },
      query: { window: '7d' },
      body: undefined,
    });

    await waitFor(() => expect(post).toHaveBeenCalled());
    const reply = post.mock.calls[0][0];
    expect(reply.type).toBe('host/data.response');
    expect(reply.id).toBe('r2');
    expect(reply.payload.ok).toBe(true);
    expect(reply.payload.data).toEqual({ rows: [{ ns: 'a' }] });
  });

  it('denies a data request for a dataSource outside the handshake allowlist without calling the proxy', async () => {
    render(<SandboxedExtension mount={mount()} manifestSha="sha256:abc" />);
    const { post, win } = stubIframeWindow();
    fireLoad();
    post.mockClear();

    dispatchMessage(
      bridge('ext/data.request', { id: 'r3', payload: { dataSource: 'secrets' } }),
      ORIGIN,
      win,
    );

    await waitFor(() => expect(post).toHaveBeenCalled());
    expect(mockedFetch).not.toHaveBeenCalled();
    const reply = post.mock.calls[0][0];
    expect(reply.payload.ok).toBe(false);
    expect(reply.payload.error.code).toBe('extension_rbac_denied');
  });
});

describe('SandboxedExtension — handshake rejection', () => {
  it('renders the incompatible placeholder when ext/ready manifestSha mismatches', async () => {
    const { container } = render(<SandboxedExtension mount={mount()} manifestSha="sha256:installed" />);
    const { win } = stubIframeWindow();
    fireLoad();

    dispatchMessage(
      bridge('ext/ready', { payload: { manifestSha: 'sha256:different', acceptsProtocol: [1] } }),
      ORIGIN,
      win,
    );

    await waitFor(() =>
      expect(container.querySelector('[data-bridge-state="incompatible"]')).toBeInTheDocument(),
    );
  });
});

describe('cspToString', () => {
  it('serializes a per-extension CSP into directive form', () => {
    expect(
      cspToString({ scriptSrc: ["'self'"], connectSrc: ["'self'"], frameSrc: ["'none'"] }),
    ).toBe("script-src 'self'; connect-src 'self'; frame-src 'none'");
  });
  it('returns empty for undefined', () => {
    expect(cspToString(undefined)).toBe('');
  });
});
