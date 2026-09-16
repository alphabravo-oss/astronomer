import type { MockedFunction } from "vitest";
import { ReactNode } from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useClusterMetricsSummary } from "@/lib/hooks/workloads";
import { getClusterMetricsSummary } from "@/lib/api/metrics";

vi.mock("@/lib/api/metrics");

const mockedGet = getClusterMetricsSummary as MockedFunction<
  typeof getClusterMetricsSummary
>;

function wrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

describe("useClusterMetricsSummary", () => {
  afterEach(() => vi.clearAllMocks());

  it("always attempts the metrics query (not feature-gated) for a cluster id", async () => {
    mockedGet.mockResolvedValue({ cpuPercentage: 12 } as never);

    const { result } = renderHook(() => useClusterMetricsSummary("c1"), {
      wrapper,
    });

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockedGet).toHaveBeenCalledWith("c1", expect.any(AbortSignal));
  });

  it("surfaces an error state when metrics are unavailable instead of swallowing it", async () => {
    mockedGet.mockRejectedValue(new Error("metrics unavailable"));

    const { result } = renderHook(() => useClusterMetricsSummary("c1"), {
      wrapper,
    });

    await waitFor(() => expect(result.current.isError).toBe(true));
  });
});
