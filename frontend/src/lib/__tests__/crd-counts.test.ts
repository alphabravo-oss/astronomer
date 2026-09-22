import { crdCountJobs, getCRDNavCounts } from "@/lib/api/crd-counts";
import { k8sProxy } from "@/lib/api/kubernetes-proxy";

vi.mock("@/lib/api/kubernetes-proxy", () => ({ k8sProxy: vi.fn() }));
const types = [
  {
    group: "cert-manager.io",
    version: "v1",
    plural: "certificates",
    namespaced: true,
  },
];
beforeEach(() => vi.mocked(k8sProxy).mockReset());

it("bounds total requests including namespace fanout without partial totals", () => {
  expect(crdCountJobs(types, [])).toEqual([]);
  expect(
    crdCountJobs(
      types,
      Array.from({ length: 16 }, (_, i) => `ns-${i}`),
    ),
  ).toEqual([]);
  const many = Array.from({ length: 40 }, (_, i) => ({
    ...types[0],
    plural: `type${i}`,
  }));
  expect(crdCountJobs(many, ["one", "two"])).toHaveLength(14);
});

it("uses metadata-only requests and sums exactly the selected namespaces", async () => {
  vi.mocked(k8sProxy).mockResolvedValue({
    kind: "PartialObjectMetadataList",
    metadata: { remainingItemCount: 4 },
    items: [{}],
  });
  expect(await getCRDNavCounts("c-1", types, ["one", "two"])).toEqual({
    "crd:cert-manager.io/certificates": 10,
  });
  expect(k8sProxy).toHaveBeenCalledWith(
    "c-1",
    "GET",
    expect.stringContaining("/namespaces/one/"),
    undefined,
    {
      Accept:
        "application/json;as=PartialObjectMetadataList;g=meta.k8s.io;v=v1",
    },
    undefined,
  );
});

it("omits a type if any namespace fails, never displaying a false zero or partial count", async () => {
  vi.mocked(k8sProxy)
    .mockResolvedValueOnce({
      kind: "PartialObjectMetadataList",
      metadata: {},
      items: [{}],
    })
    .mockRejectedValueOnce(new Error("Forbidden"));
  expect(await getCRDNavCounts("c-1", types, ["one", "two"])).toEqual({});
});

it.each([
  { kind: "CertificateList", metadata: {}, items: [] },
  {
    kind: "PartialObjectMetadataList",
    metadata: { continue: "next" },
    items: [{}],
  },
])("rejects non-metadata or incomplete responses", async (response) => {
  vi.mocked(k8sProxy).mockResolvedValue(response);
  expect(await getCRDNavCounts("c-1", types, null)).toEqual({});
});
