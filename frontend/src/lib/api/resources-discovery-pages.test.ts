import { getCompleteResourceDiscovery } from "./resources";
import { getClustersByClusterIdResourcesDiscovery } from "./generated/client";
vi.mock("./generated/client", () => ({
  getClustersByClusterIdResourcesDiscovery: vi.fn(),
}));
const page = (crds: unknown[], next = "", errors = {}) => ({
  data: {
    cluster_id: "c",
    resources: [],
    crds,
    crd_continue: next,
    partial: Object.keys(errors).length > 0,
    errors,
  },
});
it("collects metadata beyond 500 definitions and retains builtin-only partial discovery", async () => {
  const request = vi.mocked(getClustersByClusterIdResourcesDiscovery);
  request
    .mockResolvedValueOnce(
      page(
        Array.from({ length: 500 }, (_, index) => ({ name: `type-${index}` })),
        "opaque",
        { gateways: "not installed" },
      ) as never,
    )
    .mockResolvedValueOnce(page([{ name: "type-501" }]) as never);
  const result = await getCompleteResourceDiscovery("c");
  expect(result.crds).toHaveLength(501);
  expect(request.mock.lastCall?.[0].query).toEqual({
    crd_limit: 500,
    crd_continue: "opaque",
  });
});
it("rejects missing CRD discovery and a repeated continuation instead of claiming completeness", async () => {
  const request = vi.mocked(getClustersByClusterIdResourcesDiscovery);
  request.mockResolvedValueOnce(
    page([], "", { custom_resource_definitions: "Forbidden" }) as never,
  );
  await expect(getCompleteResourceDiscovery("c")).rejects.toThrow("Forbidden");
  request.mockResolvedValue(page([], "repeated") as never);
  await expect(getCompleteResourceDiscovery("c")).rejects.toThrow("repeated");
});
