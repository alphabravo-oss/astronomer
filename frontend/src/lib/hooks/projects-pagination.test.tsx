import type { PropsWithChildren } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { useProjects, useProjectSearch } from "./projects";

const getProjects = vi.hoisted(() => vi.fn());
const getClusterProjects = vi.hoisted(() => vi.fn());

vi.mock("@/lib/api/projects", () => ({
  getProjects,
  getClusterProjects,
  getProject: vi.fn(),
  createProject: vi.fn(),
  deleteProject: vi.fn(),
  takeoverProjectOwnership: vi.fn(),
}));

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper = ({ children }: PropsWithChildren) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return { wrapper };
}

function projectPage(page: number, total: number) {
  const offset = (page - 1) * 50;
  const count = Math.min(50, total - offset);
  const nextOffset = offset + count;
  return {
    data: Array.from({ length: count }, (_, index) => ({
      id: `project-${offset + index + 1}`,
      name: `project-${offset + index + 1}`,
      displayName: `Project ${offset + index + 1}`,
      clusterId: "cluster-1",
      namespaces: [],
    })),
    pagination: {
      total,
      limit: 50,
      offset,
      has_more: nextOffset < total,
      next_offset: nextOffset < total ? nextOffset : null,
    },
  };
}

beforeEach(() => vi.resetAllMocks());

it("reaches a cluster project beyond 200 through bounded server pages", async () => {
  getClusterProjects.mockImplementation(
    async (_clusterId, { page }: { page: number }) => projectPage(page, 225),
  );
  const { result } = renderHook(
    () => useProjectSearch("cluster-1", " production "),
    setup(),
  );

  await waitFor(() => expect(result.current.data?.pages).toHaveLength(1));
  for (let page = 2; page <= 5; page += 1) {
    await act(async () => {
      await result.current.fetchNextPage();
    });
    await waitFor(() => expect(result.current.data?.pages).toHaveLength(page));
  }

  const loaded = result.current.data?.pages.flatMap((page) => page.data) ?? [];
  expect(loaded).toHaveLength(225);
  expect(loaded.at(-1)?.id).toBe("project-225");
  expect(result.current.hasNextPage).toBe(false);
  expect(getClusterProjects.mock.calls.map(([, params]) => params)).toEqual(
    [1, 2, 3, 4, 5].map((page) => ({
      search: "production",
      page,
      pageSize: 50,
    })),
  );
});

it("moves the primary project list past its former first 20 rows", async () => {
  getProjects.mockImplementation(async ({ page }: { page: number }) =>
    projectPage(page, 75),
  );
  const { result, rerender } = renderHook(
    ({ page }) => useProjects({ page, pageSize: 50, search: "platform" }),
    { initialProps: { page: 1 }, ...setup() },
  );

  await waitFor(() => expect(result.current.data?.data).toHaveLength(50));
  rerender({ page: 2 });
  await waitFor(() =>
    expect(result.current.data?.data[0]?.id).toBe("project-51"),
  );
  expect(result.current.data?.data).toHaveLength(25);
  expect(getProjects.mock.calls.map(([params]) => params)).toEqual([
    { page: 1, pageSize: 50, search: "platform" },
    { page: 2, pageSize: 50, search: "platform" },
  ]);
});
