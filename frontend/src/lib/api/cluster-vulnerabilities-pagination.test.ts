import { getImageVulnReport } from "./cluster-vulnerabilities";
import { getClustersByClusterIdVulnerabilitiesReportsById } from "./generated/client";

vi.mock("./generated/client", () => ({
  getClustersByClusterIdVulnerabilitiesReportsById: vi.fn(),
}));
it.each([undefined, 126])(
  "preserves CVE continuation and optional total (%s)",
  async (total) => {
    const pagination = {
      limit: 25,
      offset: 100,
      total,
      has_more: true,
      next_offset: 125,
    };
    vi.mocked(
      getClustersByClusterIdVulnerabilitiesReportsById,
    ).mockResolvedValue({
      data: {
        report: { id: "r1" },
        vulnerabilities: {
          data: [
            {
              id: "v1",
              report_id: "r1",
              vulnerability_id: "CVE-2026-1234",
              severity: "HIGH",
              pkg_name: "package",
            },
          ],
          pagination,
        },
        severity_filter: "HIGH",
      },
    } as never);
    const signal = new AbortController().signal;
    const result = await getImageVulnReport(
      "c1",
      "r1",
      { severity: "HIGH", offset: 100, limit: 25 },
      signal,
    );
    expect(result.vulnerabilities).toEqual({
      data: [
        expect.objectContaining({
          id: "v1",
          reportId: "r1",
          vulnerabilityId: "CVE-2026-1234",
          severity: "HIGH",
          pkgName: "package",
        }),
      ],
      pagination,
    });
    expect(result).not.toHaveProperty("vulnerabilityTotal");
    expect(
      getClustersByClusterIdVulnerabilitiesReportsById,
    ).toHaveBeenLastCalledWith({
      path: { cluster_id: "c1", id: "r1" },
      query: { severity: "HIGH", offset: 100, limit: 25 },
      signal,
    });
  },
);
