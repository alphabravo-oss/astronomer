import {
  adminComplianceBaselineApply,
  adminComplianceBaselineDiff,
  adminComplianceBaselinesList,
} from "@/lib/api/generated/client";
import {
  applyComplianceBaseline,
  getComplianceBaselineDiff,
  listComplianceBaselines,
} from "./settings-compliance-baselines";

vi.mock("@/lib/api/generated/client", () => ({
  adminComplianceBaselineActive: vi.fn(),
  adminComplianceBaselineApplicationRevert: vi.fn(),
  adminComplianceBaselineApplicationsList: vi.fn(),
  adminComplianceBaselineApply: vi.fn(),
  adminComplianceBaselineDiff: vi.fn(),
  adminComplianceBaselineGet: vi.fn(),
  adminComplianceBaselinesList: vi.fn(),
}));

const baseline = {
  id: "00000000-0000-4000-8000-000000000001",
  slug: "soc2" as const,
  name: "SOC 2",
  description: "SOC 2 controls",
  version: "1",
  enabled: true,
  active: false,
  spec: { audit_retention_days: 365, required_totp: true },
};

describe("generated compliance baseline API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("preserves required_totp and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(adminComplianceBaselinesList).mockResolvedValueOnce({
      data: [baseline],
    });

    await expect(listComplianceBaselines({ signal })).resolves.toEqual([
      expect.objectContaining({
        spec: expect.objectContaining({ required_totp: true }),
      }),
    ]);
    expect(adminComplianceBaselinesList).toHaveBeenCalledWith({ signal });
  });

  it("maps diff wire casing and uses the synchronous apply receipt", async () => {
    vi.mocked(adminComplianceBaselineDiff).mockResolvedValueOnce({
      data: {
        baseline_id: baseline.id,
        baseline_slug: baseline.slug,
        baseline_name: baseline.name,
        current: {},
        target: {},
        changes: [],
      },
    });
    vi.mocked(adminComplianceBaselineApply).mockResolvedValueOnce({
      data: {
        application_id: "app-1",
        baseline_id: baseline.id,
        slug: baseline.slug,
      },
    });

    await expect(getComplianceBaselineDiff(baseline.id)).resolves.toEqual(
      expect.objectContaining({ baselineName: "SOC 2" }),
    );
    await expect(
      applyComplianceBaseline(baseline.id, "approved"),
    ).resolves.toEqual({
      application_id: "app-1",
      baseline_id: baseline.id,
      slug: "soc2",
    });
    expect(adminComplianceBaselineApply).toHaveBeenCalledWith({
      path: { id: baseline.id },
      body: { notes: "approved" },
      signal: undefined,
    });
  });
});
