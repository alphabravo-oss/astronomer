import type { MockedFunction } from 'vitest';
import { ReactNode } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as extensionsApi from '@/lib/api/extensions';
import {
  ExtensionProvider,
  useExtensionMounts,
  useExtensionRuntime,
  useExtensionTheme,
  type ExtensionTheme,
} from './ExtensionProvider';
import type { ExtensionMount, ExtensionMountsResponse } from '@/lib/api/extensions';

vi.mock('@/lib/api/extensions', () => ({
  __esModule: true,
  getExtensionMounts: vi.fn(),
}));

const mockedMounts = extensionsApi.getExtensionMounts as MockedFunction<
  typeof extensionsApi.getExtensionMounts
>;

function tab(pointId: string): ExtensionMount {
  return {
    extension: 'cost',
    displayName: 'Cost',
    point: 'clusterTab',
    pointId,
    tier: 1,
    render: { declarative: { kind: 'table', dataSource: 'd1' } },
  };
}

function response(over: Partial<ExtensionMountsResponse> = {}): ExtensionMountsResponse {
  return { sidebar: [], dashboardWidgets: [], clusterTabs: [], settings: [], ...over };
}

function makeWrapper(theme?: ExtensionTheme) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return function wrapper({ children }: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        <ExtensionProvider theme={theme}>{children}</ExtensionProvider>
      </QueryClientProvider>
    );
  };
}

describe('ExtensionProvider runtime', () => {
  afterEach(() => vi.clearAllMocks());

  it('exposes the indexed registry to useExtensionMounts(point)', async () => {
    mockedMounts.mockResolvedValue(response({ clusterTabs: [tab('a'), tab('b')] }));

    const { result } = renderHook(() => useExtensionMounts('clusterTab'), {
      wrapper: makeWrapper(),
    });

    await waitFor(() => expect(result.current).toHaveLength(2));
    expect(result.current.map((m) => m.pointId)).toEqual(['a', 'b']);
  });

  it('returns an empty list for a point with no mounts', async () => {
    mockedMounts.mockResolvedValue(response({ clusterTabs: [tab('a')] }));

    const { result } = renderHook(() => useExtensionMounts('sidebar'), {
      wrapper: makeWrapper(),
    });

    // sidebar has no mounts; once loaded it is a stable empty array.
    await waitFor(() => expect(extensionsApi.getExtensionMounts).toHaveBeenCalled());
    expect(result.current).toEqual([]);
  });

  it('surfaces the loading/error state of the underlying query', async () => {
    mockedMounts.mockRejectedValue(new Error('mounts unavailable'));

    const { result } = renderHook(() => useExtensionRuntime(), { wrapper: makeWrapper() });

    await waitFor(() => expect(result.current.isError).toBe(true));
    // Registry stays a safe empty registry even on error (fail-closed).
    expect(result.current.registry.clusterTab).toEqual([]);
  });

  it('passes the host theme tokens through for the Tier-2 handshake', async () => {
    mockedMounts.mockResolvedValue(response());
    const theme: ExtensionTheme = { mode: 'dark', tokens: { '--background': '#000' } };

    const { result } = renderHook(() => useExtensionTheme(), { wrapper: makeWrapper(theme) });

    await waitFor(() => expect(result.current).toEqual(theme));
  });
});

describe('useExtensionRuntime without a provider', () => {
  it('degrades to an empty runtime instead of throwing (fail-closed)', () => {
    const { result } = renderHook(() => useExtensionRuntime());

    expect(result.current.isLoading).toBe(false);
    expect(result.current.isError).toBe(false);
    expect(result.current.registry.sidebar).toEqual([]);
    expect(result.current.theme).toBeUndefined();
  });
});
