import {
  deleteClustersByIdGatekeeperConstraintsByName,
  getClustersByIdGatekeeperConstraints,
  postClustersByIdGatekeeperConstraints,
  postClustersByIdGatekeeperConstraintsValidate,
} from "@/lib/api/generated/client";
import {
  applyGatekeeperConstraint,
  deleteGatekeeperConstraint,
  listGatekeeperConstraints,
  validateGatekeeperConstraint,
} from "@/lib/api/gatekeeper-constraints";

vi.mock("@/lib/api/generated/client", () => ({
  deleteClustersByIdGatekeeperConstraintsByName: vi.fn(),
  getClustersByIdGatekeeperConstraints: vi.fn(),
  postClustersByIdGatekeeperConstraints: vi.fn(),
  postClustersByIdGatekeeperConstraintsValidate: vi.fn(),
}));

const validationWire = {
  valid: true,
  errors: [],
  applied: false,
  name: "require-team-label",
  kind: "K8sRequiredLabels",
};

describe("Gatekeeper generated API boundary", () => {
  beforeEach(() => vi.clearAllMocks());

  it("flattens bundle and custom groups without losing their source", async () => {
    vi.mocked(getClustersByIdGatekeeperConstraints).mockResolvedValueOnce({
      data: {
        bundle: [
          {
            name: "bundle-labels",
            kind: "K8sRequiredLabels",
            api_version: "constraints.gatekeeper.sh/v1beta1",
            enforcement_action: "deny",
          },
        ],
        custom: [
          {
            name: "custom-labels",
            kind: "K8sRequiredLabels",
            api_version: "constraints.gatekeeper.sh/v1beta1",
            violation_count: 2,
            yaml: "apiVersion: constraints.gatekeeper.sh/v1beta1",
          },
        ],
      },
    });

    await expect(listGatekeeperConstraints("cluster-1")).resolves.toEqual([
      expect.objectContaining({
        name: "bundle-labels",
        source: "bundle",
        enforcementAction: "deny",
        violationCount: 0,
      }),
      expect.objectContaining({
        name: "custom-labels",
        source: "custom",
        violationCount: 2,
      }),
    ]);
  });

  it("serializes validation and maps its envelope", async () => {
    vi.mocked(
      postClustersByIdGatekeeperConstraintsValidate,
    ).mockResolvedValueOnce({ data: validationWire });

    await expect(
      validateGatekeeperConstraint("cluster-1", "yaml"),
    ).resolves.toEqual(validationWire);
    expect(postClustersByIdGatekeeperConstraintsValidate).toHaveBeenCalledWith({
      path: { id: "cluster-1" },
      body: { yaml: "yaml" },
    });
  });

  it("serializes apply and maps its envelope", async () => {
    vi.mocked(postClustersByIdGatekeeperConstraints).mockResolvedValueOnce({
      data: { ...validationWire, status: "pending", task_id: "task-1" },
    });

    await expect(
      applyGatekeeperConstraint("cluster-1", "yaml"),
    ).resolves.toEqual({
      ...validationWire,
      status: "pending",
      taskId: "task-1",
    });
  });

  it("passes identifiers to the generated delete path", async () => {
    vi.mocked(
      deleteClustersByIdGatekeeperConstraintsByName,
    ).mockResolvedValueOnce({
      data: { name: "require labels", status: "pending", task_id: "task-2" },
    });
    await expect(
      deleteGatekeeperConstraint("cluster-1", "require labels"),
    ).resolves.toEqual({
      name: "require labels",
      status: "pending",
      task_id: "task-2",
    });
    expect(deleteClustersByIdGatekeeperConstraintsByName).toHaveBeenCalledWith({
      path: { id: "cluster-1", name: "require labels" },
      headerParams: { "Idempotency-Key": expect.any(String) },
    });
  });
});
