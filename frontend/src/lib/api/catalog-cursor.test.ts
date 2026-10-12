import { getHelmCharts } from "./catalog";
import { getCatalogCharts } from "@/lib/api/generated/client";

vi.mock("@/lib/api/generated/client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api/generated/client")>()),
  getCatalogCharts: vi.fn(),
}));

beforeEach(() => vi.clearAllMocks());

it("preserves explicit initial and opaque continuation queries, scope and cancellation", async () => {
  const signal = new AbortController().signal;
  for (const cursor of ["", "opaque-past-ten-thousand_+/="]) {
    const pagination = {
      limit: 25,
      offset: 10025,
      has_more: false,
      next_offset: null,
      next_cursor: null,
    };
    vi.mocked(getCatalogCharts).mockResolvedValueOnce({ data: [], pagination });
    await expect(
      getHelmCharts(
        { projectId: "project", search: " literal ", cursor },
        signal,
      ),
    ).resolves.toEqual({ data: [], pagination });
    expect(getCatalogCharts).toHaveBeenLastCalledWith({
      query: {
        cluster_id: undefined,
        project_id: "project",
        search: "literal",
        tag: undefined,
        limit: 25,
        offset: undefined,
        cursor,
      },
      signal,
    });
  }
});

it("rejects mixed or oversized cursors without reaching generated transport", async () => {
  await expect(getHelmCharts({ cursor: "", offset: 0 })).rejects.toThrow(
    "without offset",
  );
  await expect(getHelmCharts({ cursor: "a".repeat(4097) })).rejects.toThrow(
    "bounded value",
  );
  expect(getCatalogCharts).not.toHaveBeenCalled();
});
