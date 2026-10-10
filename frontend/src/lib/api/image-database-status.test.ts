import { describe, expect, it, vi } from "vitest";
import { getImageDatabaseStatus } from "./cluster-vulnerabilities";
import { getClustersByClusterIdVulnerabilitiesReportsByIdDatabaseStatus } from "./generated/client";
import { validateCategoricalResponse } from "./categorical-values";

vi.mock("./generated/client", () => ({
  getClustersByClusterIdVulnerabilitiesReportsByIdDatabaseStatus: vi.fn(),
}));

const status = {
  report_id: "58ef5e30-7d56-4a7d-9775-b764d895f154",
  scan_state: "complete", main_state: "fresh", java_state: "unknown",
  java_applicability: "unknown", reason: "current_workload_unchecked",
  finding_count: 2, observation_revision: 5, witness_revision: 5,
  cycle: 2, accepted_generation: 3, report_expired: false,
  rescan_required: false, eligible_for_clean_posture: false,
  current_workload_checked: false, resource_capacity_exhausted: false,
};

describe("additive image database status boundary", () => {
  it("uses the generated operation with exact scoped IDs and cancellation", async () => {
    vi.mocked(getClustersByClusterIdVulnerabilitiesReportsByIdDatabaseStatus)
      .mockResolvedValue({ data: status } as never);
    const signal = new AbortController().signal;
    const result = await getImageDatabaseStatus("cluster-id", status.report_id, signal);
    expect(getClustersByClusterIdVulnerabilitiesReportsByIdDatabaseStatus)
      .toHaveBeenLastCalledWith({ path: { cluster_id: "cluster-id", id: status.report_id }, signal });
    expect(result).toBe(status);
    expect(result.finding_count).toBe(2);
    expect(result.current_workload_checked).toBe(false);
  });

  it("rejects a missing status payload with static operation context", async () => {
    vi.mocked(getClustersByClusterIdVulnerabilitiesReportsByIdDatabaseStatus)
      .mockResolvedValue({} as never);
    await expect(getImageDatabaseStatus("cluster-id", status.report_id))
      .rejects.toThrow("Image database status returned an empty data envelope");
  });

  it.each(["scan_state", "main_state", "java_state", "java_applicability"] as const)(
    "normalizes unsupported %s without promoting it to a known state", (field) => {
      const checked = validateCategoricalResponse(
        "getClustersByClusterIdVulnerabilitiesReportsByIdDatabaseStatus", 200,
        { data: { ...status, [field]: "unknown-supplier-secret-value" } },
      );
      expect(checked).toEqual({ data: { ...status, [field]: "unsupported" } });
      expect(JSON.stringify(checked)).not.toContain("unknown-supplier-secret-value");
    },
  );
});
