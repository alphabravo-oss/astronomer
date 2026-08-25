import * as generated from "@/lib/api/generated/client";
import type { OpenAPIComponents } from "@/types/openapi.generated";
import {
  createCISScan,
  getCISProfiles,
  getCISScan,
  getCISScans,
} from "./security-scans";

vi.mock("@/lib/api/generated/client", () => ({
  getSecurityProfiles: vi.fn(),
  getSecurityScans: vi.fn(),
  getSecurityScansById: vi.fn(),
  postSecurityScans: vi.fn(),
}));

type Schemas = OpenAPIComponents["schemas"];

const scanWire: Schemas["CISScan"] = {
  id: "scan-1",
  cluster_id: "cluster-1",
  scan_type: "cis-1.8",
  status: "running",
  passed: 12,
  failed: 2,
  warned: 1,
  skipped: 3,
  started_at: "2026-08-23T00:00:00Z",
  completed_at: null,
  terminal_reason: "",
  findings: [
    {
      test_id: "1.2.3",
      severity: "high",
      status: "fail",
      description: "Secure the API server",
      remediation: "Set the documented flag",
    },
  ],
  created_at: "2026-08-23T00:00:00Z",
  updated_at: "2026-08-23T00:01:00Z",
};

describe("CIS generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("uses the required cluster query and preserves the profile wire contract", async () => {
    vi.mocked(generated.getSecurityProfiles).mockResolvedValueOnce({
      data: {
        items: [{ name: "cis-1.8", benchmarkVersion: "cis-1.8" }],
        source: "cluster",
      },
    });
    await expect(getCISProfiles("cluster-1")).resolves.toEqual({
      items: [{ name: "cis-1.8", benchmarkVersion: "cis-1.8" }],
      source: "cluster",
    });
    expect(generated.getSecurityProfiles).toHaveBeenCalledWith({
      query: { cluster_id: "cluster-1" },
    });
  });

  it("maps scan pages and finding fields without global camelization", async () => {
    vi.mocked(generated.getSecurityScans).mockResolvedValueOnce({
      data: [scanWire],
      count: 61,
      next: "next",
      previous: null,
    });
    await expect(getCISScans({ page: 3, pageSize: 20 })).resolves.toEqual(
      expect.objectContaining({
        total: 61,
        page: 3,
        totalPages: 4,
        data: [
          expect.objectContaining({
            clusterId: "cluster-1",
            scanType: "cis-1.8",
            findings: [
              expect.objectContaining({ testId: "1.2.3", status: "fail" }),
            ],
          }),
        ],
      }),
    );
    expect(generated.getSecurityScans).toHaveBeenCalledWith({
      query: { limit: 20, offset: 40 },
    });
  });

  it("uses generated object and create operations", async () => {
    vi.mocked(generated.getSecurityScansById).mockResolvedValueOnce({
      data: scanWire,
    });
    await expect(getCISScan("scan-1")).resolves.toEqual(
      expect.objectContaining({ id: "scan-1", status: "running" }),
    );
    expect(generated.getSecurityScansById).toHaveBeenCalledWith({
      path: { id: "scan-1" },
    });

    vi.mocked(generated.postSecurityScans).mockResolvedValueOnce({
      data: scanWire,
    });
    await createCISScan({ cluster_id: "cluster-1", profile: "cis-1.8" });
    expect(generated.postSecurityScans).toHaveBeenCalledWith({
      body: { cluster_id: "cluster-1", profile: "cis-1.8" },
    });
  });
});
