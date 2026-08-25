import {
  executeOpenAPIOperationWithResponse,
  getClustersByIdRegistrationStatus,
  putClustersByIdRegistrationOptions,
} from "@/lib/api/generated/client";
import {
  getClusterManifestWithToken,
  getRegistrationStatus,
  setRegistrationOptions,
} from "./cluster-registration";

vi.mock("@/lib/api/generated/client", () => ({
  executeOpenAPIOperationWithResponse: vi.fn(),
  getClustersByIdRegistrationStatus: vi.fn(),
  postClustersByIdRegistrationCancel: vi.fn(),
  postClustersByIdRegistrationConfirm: vi.fn(),
  postClustersByIdRegistrationRetryByStepId: vi.fn(),
  putClustersByIdRegistrationOptions: vi.fn(),
}));

const statusWire = {
  cluster_id: "00000000-0000-4000-8000-000000000001",
  phase: "provisioning" as const,
  install_baseline: true,
  started_at: "2026-08-24T00:00:00Z",
  steps: [
    {
      id: "00000000-0000-4000-8000-000000000002",
      step_name: "agent",
      label: "Connect agent",
      status: "success" as const,
      progress_pct: 100,
      step_order: 1,
      created_at: "2026-08-24T00:00:00Z",
    },
  ],
};

describe("generated cluster registration API", () => {
  beforeEach(() => vi.clearAllMocks());

  it("maps registration wire fields and propagates cancellation", async () => {
    const signal = new AbortController().signal;
    vi.mocked(getClustersByIdRegistrationStatus).mockResolvedValueOnce({
      data: statusWire,
    });

    await expect(
      getRegistrationStatus(statusWire.cluster_id, { signal }),
    ).resolves.toEqual(
      expect.objectContaining({
        clusterId: statusWire.cluster_id,
        installBaseline: true,
        steps: [
          expect.objectContaining({ stepName: "agent", progressPct: 100 }),
        ],
      }),
    );
    expect(getClustersByIdRegistrationStatus).toHaveBeenCalledWith({
      path: { id: statusWire.cluster_id },
      signal,
    });
  });

  it("sends the exact generated options body", async () => {
    vi.mocked(putClustersByIdRegistrationOptions).mockResolvedValueOnce({
      data: statusWire,
    });

    await setRegistrationOptions(statusWire.cluster_id, true);

    expect(putClustersByIdRegistrationOptions).toHaveBeenCalledWith({
      path: { id: statusWire.cluster_id },
      body: { install_baseline: true },
      signal: undefined,
    });
  });

  it("uses the generated text operation while retaining the token header", async () => {
    const signal = new AbortController().signal;
    vi.mocked(executeOpenAPIOperationWithResponse).mockResolvedValueOnce({
      data: "apiVersion: v1",
      headers: { "x-astronomer-registration-token": "token-1" },
      status: 200,
    });

    await expect(
      getClusterManifestWithToken(statusWire.cluster_id, { signal }),
    ).resolves.toEqual({ manifest: "apiVersion: v1", token: "token-1" });
    expect(executeOpenAPIOperationWithResponse).toHaveBeenCalledWith(
      "getClustersByIdManifest",
      { path: { id: statusWire.cluster_id }, signal },
    );
  });
});
