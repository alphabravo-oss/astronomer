import { replicaRedundancy } from "./system-component-availability";

describe("replica redundancy", () => {
  it.each([
    "Distribution",
    "GatewayInventory",
    "CertificateInventory",
    "LonghornNodeInventory",
  ])("does not infer topology from %s", (kind) => {
    expect(replicaRedundancy({ kind, highAvailability: true })).toBe(
      "Not observed",
    );
  });
  it("labels workload replicas without claiming independent failure domains", () => {
    expect(
      replicaRedundancy({ kind: "Deployment", highAvailability: true }),
    ).toBe("Multiple replicas");
  });
});
