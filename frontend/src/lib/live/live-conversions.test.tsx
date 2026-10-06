import { renderHook, waitFor, act } from "@testing-library/react";
import {
  QueryClient,
  QueryClientProvider,
  useQuery,
} from "@tanstack/react-query";
import type { ReactNode } from "react";

vi.mock("@/lib/api/auth", () => ({
  createStreamTicket: vi.fn().mockResolvedValue({ ticket: "tkt" }),
}));

import { useLiveEvents } from "@/lib/live/hooks";
import { useLiveClusterMetricsMerger } from "@/lib/live/cluster-merger";
import { liveFallback } from "@/lib/live/status-store";
import { queryKeys } from "@/lib/query-keys";
import { catalogOperationPollInterval } from "@/components/catalog/catalog-operation-timeline";
import type { CatalogOperation } from "@/lib/api/catalog";

class FakeEventSource {
  static instances: FakeEventSource[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string) {
    FakeEventSource.instances.push(this);
  }
  close(): void {
    /* no-op */
  }
  emitFrame(frame: unknown): void {
    this.onmessage?.({ data: JSON.stringify(frame) });
  }
}

const lastSource = () =>
  FakeEventSource.instances[FakeEventSource.instances.length - 1];

function wrapperFor(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

beforeEach(() => {
  FakeEventSource.instances = [];
  (globalThis as unknown as { EventSource: unknown }).EventSource =
    FakeEventSource;
});

describe("live conversions", () => {
  it("a stream event updates a cached list row without a refetch", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const queryFn = vi.fn().mockResolvedValue({
      data: [{ id: "c1", name: "prod", status: "active" }],
    });
    const key = queryKeys.clusters.list({});
    const { result, unmount } = renderHook(
      () => {
        useLiveEvents();
        useLiveClusterMetricsMerger();
        return useQuery({
          queryKey: key,
          queryFn,
          refetchInterval: liveFallback(30_000),
        });
      },
      { wrapper: wrapperFor(qc) },
    );
    await waitFor(() => expect(result.current.data).toBeDefined());
    await waitFor(() => expect(FakeEventSource.instances.length).toBe(1));
    act(() => lastSource().onopen?.());
    expect(queryFn).toHaveBeenCalledTimes(1);

    act(() =>
      lastSource().emitFrame({
        id: 1,
        type: "cluster.status_changed",
        time: "t",
        data: {
          cluster_id: "c1",
          old_status: "active",
          new_status: "degraded",
        },
      }),
    );

    await waitFor(() =>
      expect(
        (result.current.data as { data: { status: string }[] }).data[0].status,
      ).toBe("degraded"),
    );
    expect(queryFn).toHaveBeenCalledTimes(1);
    unmount();
  });

  it("falls back to polling when the stream fails", async () => {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const queryFn = vi.fn().mockResolvedValue({ ok: true });
    const { result, unmount } = renderHook(
      () => {
        useLiveEvents();
        return useQuery({
          queryKey: queryKeys.charlie.findings,
          queryFn,
          refetchInterval: liveFallback(40),
        });
      },
      { wrapper: wrapperFor(qc) },
    );
    await waitFor(() => expect(result.current.data).toBeDefined());
    await waitFor(() => expect(FakeEventSource.instances.length).toBe(1));
    act(() => lastSource().onopen?.());

    // Stream open: polling is off.
    // Let any poll scheduled before the stream opened drain, then confirm
    // the call count stays flat while the stream is open.
    await new Promise((r) => setTimeout(r, 150));
    const whileOpen = queryFn.mock.calls.length;
    await new Promise((r) => setTimeout(r, 150));
    expect(queryFn).toHaveBeenCalledTimes(whileOpen);

    // Stream drops: the open->closed transition re-evaluates the interval
    // and polling resumes.
    act(() => lastSource().onerror?.());
    await waitFor(
      () => expect(queryFn.mock.calls.length).toBeGreaterThan(whileOpen),
      {
        timeout: 2000,
      },
    );
    unmount();
  });

  it("catalog operations poll until settled regardless of stream state", () => {
    expect(
      catalogOperationPollInterval({ status: "running" } as CatalogOperation),
    ).toBe(2_500);
    expect(
      catalogOperationPollInterval({ status: "completed" } as CatalogOperation),
    ).toBe(false);
  });
});
