import { ReactNode } from 'react';
import { renderHook, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useClusterMetricsSummary } from '@/lib/hooks';
import * as api from '@/lib/api';

jest.mock('@/lib/api');

const mockedGet = api.getClusterMetricsSummary as jest.MockedFunction<
  typeof api.getClusterMetricsSummary
>;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe('useClusterMetricsSummary', () => {
  afterEach(() => jest.clearAllMocks());

  it('always attempts the metrics query (not feature-gated) for a cluster id', async () => {
    mockedGet.mockResolvedValue({ cpuPercentage: 12 } as never);

    const { result } = renderHook(() => useClusterMetricsSummary('c1'), { wrapper });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockedGet).toHaveBeenCalledWith('c1');
  });

  it('surfaces an error state when metrics are unavailable instead of swallowing it', async () => {
    mockedGet.mockRejectedValue(new Error('metrics unavailable'));

    const { result } = renderHook(() => useClusterMetricsSummary('c1'), { wrapper });

    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
