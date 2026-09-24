import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  NamespacePageControls,
  useNamespaceQueries,
} from "./namespace-queries";
import { getNamedResources } from "@/lib/api/kubernetes-resources";
import { getGenericResources } from "@/lib/api/resource-search";
import {
  getClusterNamespaces,
  getClusterEvents,
  getClusterPods,
  getWorkloads,
} from "@/lib/api/workloads";

vi.mock("@/lib/api/kubernetes-resources", () => ({
  getNamedResources: vi.fn(),
}));
vi.mock("@/lib/api/resource-search", () => ({ getGenericResources: vi.fn() }));
vi.mock("@/lib/api/workloads", () => ({
  getClusterNamespaces: vi.fn(),
  getClusterEvents: vi.fn(),
  getClusterPods: vi.fn(),
  getWorkloads: vi.fn(),
}));
const empty = {
  data: [],
  pagination: { limit: 25, offset: 0, has_more: false, next_offset: null },
};
beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(getNamedResources).mockResolvedValue(empty);
  vi.mocked(getGenericResources).mockResolvedValue(empty);
  vi.mocked(getClusterPods).mockResolvedValue(empty);
  vi.mocked(getWorkloads).mockResolvedValue(empty);
  vi.mocked(getClusterNamespaces).mockResolvedValue([]);
  vi.mocked(getClusterEvents).mockResolvedValue([]);
});
function Harness({ namespace = "team-a" }: { namespace?: string }) {
  const queries = useNamespaceQueries("cluster-a", namespace);
  return (
    <>
      <output data-testid="rows">
        {queries.configMaps.data?.data.map((row) => row.name).join(",")}
      </output>
      <NamespacePageControls queries={queries} tab="configuration" />
    </>
  );
}
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  const view = render(
    <QueryClientProvider client={client}>
      <Harness />
    </QueryClientProvider>,
  );
  return { ...view, client };
}
it("sends namespace scope and bounded pages to every resource API", async () => {
  setup();
  await waitFor(() => expect(getGenericResources).toHaveBeenCalledTimes(7));
  for (const [, , params] of vi.mocked(getGenericResources).mock.calls)
    expect(params).toMatchObject({
      namespace: "team-a",
      limit: 25,
      offset: 0,
      signal: expect.any(AbortSignal),
    });
  for (const [, , params] of vi.mocked(getNamedResources).mock.calls)
    expect(params).toMatchObject({
      namespace: "team-a",
      limit: 25,
      offset: 0,
      signal: expect.any(AbortSignal),
    });
  expect(getWorkloads).toHaveBeenCalledWith(
    "cluster-a",
    expect.objectContaining({ namespace: "team-a", offset: 0, pageSize: 25 }),
  );
  expect(getClusterPods).toHaveBeenCalledWith(
    "cluster-a",
    expect.objectContaining({ namespace: "team-a", limit: 25 }),
  );
});
it("follows each resource's actual offset independently and recovers from denied pages", async () => {
  vi.mocked(getGenericResources).mockImplementation(async (_, type, params) => {
    if (type !== "configmaps") return empty;
    if (params?.offset === 225) throw { response: { status: 403 } };
    return {
      data: [{ name: "visible-config", namespace: "team-a" }],
      pagination: { limit: 25, offset: 0, has_more: true, next_offset: 225 },
    } as Awaited<ReturnType<typeof getGenericResources>>;
  });
  setup();
  await waitFor(() =>
    expect(screen.getByTestId("rows")).toHaveTextContent("visible-config"),
  );
  fireEvent.click(screen.getByRole("button", { name: "Next configmaps page" }));
  await screen.findByText("Permission required");
  expect(screen.getByTestId("rows")).toBeEmptyDOMElement();
  expect(getGenericResources).toHaveBeenCalledWith(
    "cluster-a",
    "configmaps",
    expect.objectContaining({ offset: 225 }),
  );
  expect(
    vi
      .mocked(getGenericResources)
      .mock.calls.filter(
        ([, type, params]) => type === "secrets" && params?.offset !== 0,
      ),
  ).toHaveLength(0);
  fireEvent.click(
    screen.getByRole("button", { name: "Previous configmaps page" }),
  );
  await waitFor(() =>
    expect(screen.getByTestId("rows")).toHaveTextContent("visible-config"),
  );
});
