import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { PropsWithChildren } from "react";
import {
  getProject,
  getProjects,
  getClusterProjects,
} from "@/lib/api/projects";
import type { Project } from "@/types";
import { useCatalogProjectScope } from "./project-scope";

vi.mock("@/lib/api/projects", () => ({
  getProject: vi.fn(),
  getProjects: vi.fn(),
  getClusterProjects: vi.fn(),
}));
const project = {
  id: "project-225",
  name: "Last project",
  clusterId: "cluster-1",
  clusterIds: ["cluster-1"],
} as Project;
const page = (hasMore = false) => ({
  data: [project],
  pagination: {
    limit: 2,
    offset: 0,
    has_more: hasMore,
    next_offset: hasMore ? 2 : null,
  },
});
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return {
    wrapper: ({ children }: PropsWithChildren) => (
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    ),
  };
}
beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(getProject).mockResolvedValue(project);
  vi.mocked(getProjects).mockResolvedValue(page());
  vi.mocked(getClusterProjects).mockResolvedValue(page());
});
it("resolves a deep link beyond the old cap without enumerating projects", async () => {
  const { result } = renderHook(
    () => useCatalogProjectScope("project-225", "cluster-1"),
    setup(),
  );
  await waitFor(() => expect(result.current.projectId).toBe("project-225"));
  expect(getProject).toHaveBeenCalledWith("project-225", {
    signal: expect.any(AbortSignal),
  });
  expect(getProjects).not.toHaveBeenCalled();
  expect(getClusterProjects).not.toHaveBeenCalled();
});
it.each([undefined, "cluster-1"])(
  "auto-selects only a proven unique project in scope %s",
  async (clusterId) => {
    const { result } = renderHook(
      () => useCatalogProjectScope("", clusterId),
      setup(),
    );
    await waitFor(() => expect(result.current.projectId).toBe(project.id));
    const args = [
      { page: 1, pageSize: 2 },
      { signal: expect.any(AbortSignal) },
    ];
    if (clusterId)
      expect(getClusterProjects).toHaveBeenCalledWith(clusterId, ...args);
    else expect(getProjects).toHaveBeenCalledWith(...args);
  },
);
it("does not mistake a short nonterminal page for the only project", async () => {
  vi.mocked(getProjects).mockResolvedValue(page(true));
  const { result } = renderHook(() => useCatalogProjectScope(""), setup());
  await waitFor(() => expect(result.current.query.isSuccess).toBe(true));
  expect(result.current.projectId).toBe("");
});
it("rejects a deep link from a different cluster without selecting a fallback", async () => {
  const { result } = renderHook(
    () => useCatalogProjectScope(project.id, "cluster-2"),
    setup(),
  );
  await waitFor(() => expect(result.current.wrongCluster).toBe(true));
  expect(result.current.projectId).toBe("");
  expect(getClusterProjects).not.toHaveBeenCalled();
});
it.each([403, 404, 500])(
  "clears the selected scope after a %s refetch, including cached data",
  async (status) => {
    const { result } = renderHook(
      () => useCatalogProjectScope(project.id),
      setup(),
    );
    await waitFor(() => expect(result.current.projectId).toBe(project.id));
    vi.mocked(getProject).mockRejectedValue({ status });
    await act(async () => {
      await result.current.query.refetch();
    });
    await waitFor(() => expect(result.current.query.isError).toBe(true));
    expect(result.current.project).toBeUndefined();
    expect(result.current.projectId).toBe("");
    expect(getProjects).not.toHaveBeenCalled();
  },
);
it("clears an old scope while a new deep link loads", async () => {
  const { result, rerender } = renderHook(
    ({ id }) => useCatalogProjectScope(id),
    { ...setup(), initialProps: { id: project.id } },
  );
  await waitFor(() => expect(result.current.projectId).toBe(project.id));
  vi.mocked(getProject).mockImplementation(() => new Promise(() => {}));
  rerender({ id: "another-project" });
  expect(result.current.projectId).toBe("");
});
