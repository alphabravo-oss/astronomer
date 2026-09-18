import type { PropsWithChildren } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useClusterSearch } from "./cluster-search";

const getClusters = vi.hoisted(() => vi.fn());
vi.mock("@/lib/api/clusters", () => ({ getClusters }));

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { wrapper };
}

beforeEach(() => vi.resetAllMocks());

it("reaches clusters beyond the first 100 using bounded server pages", async () => {
  getClusters.mockImplementation(async ({ page }) => ({
    data: Array.from({ length: 50 }, (_, index) => ({
      id: `page-${page}-${index}`,
    })),
    pagination: {
      total: 150,
      limit: 50,
      offset: (page - 1) * 50,
      has_more: page < 3,
      next_offset: page < 3 ? page * 50 : null,
    },
  }));
  const { result } = renderHook(() => useClusterSearch(" prod "), setup());
  await waitFor(() => expect(result.current.data?.pages).toHaveLength(1));
  expect(result.current.hasNextPage).toBe(true);
  await act(async () => {
    await result.current.fetchNextPage();
  });
  expect(getClusters.mock.calls.map(([params]) => params.page)).toEqual([1, 2]);
  expect(result.current.error).toBeNull();
  await waitFor(() => expect(result.current.data?.pages).toHaveLength(2));
  await act(async () => {
    await result.current.fetchNextPage();
  });
  await waitFor(() => expect(result.current.data?.pages).toHaveLength(3));
  expect(getClusters.mock.calls.map(([params]) => params)).toEqual([
    { search: "prod", page: 1, pageSize: 50 },
    { search: "prod", page: 2, pageSize: 50 },
    { search: "prod", page: 3, pageSize: 50 },
  ]);
  expect(result.current.hasNextPage).toBe(false);
});

it("starts a new search at page one and cancels stale requests", async () => {
  getClusters.mockImplementation(({ search }) =>
    search === "old"
      ? new Promise(() => {})
      : Promise.resolve({
          data: [{ id: "new-cluster" }],
          pagination: {
            total: 1,
            limit: 50,
            offset: 0,
            has_more: false,
            next_offset: null,
          },
        }),
  );
  const { result, rerender } = renderHook(
    ({ search }) => useClusterSearch(search),
    { initialProps: { search: "old" }, ...setup() },
  );
  await waitFor(() => expect(getClusters).toHaveBeenCalledTimes(1));
  const signal = getClusters.mock.calls[0][1] as AbortSignal;
  rerender({ search: "new" });
  await waitFor(() => expect(result.current.isSuccess).toBe(true));
  expect(signal.aborted).toBe(true);
  expect(result.current.data?.pages[0].data).toEqual([{ id: "new-cluster" }]);
});

it("does not list the estate while the switcher is closed", () => {
  renderHook(() => useClusterSearch("", false), setup());
  expect(getClusters).not.toHaveBeenCalled();
});
