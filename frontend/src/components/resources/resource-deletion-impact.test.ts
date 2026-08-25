import {
  nodeDrainImpact,
  resourceDeletionImpact,
} from "./resource-deletion-impact";

describe("resourceDeletionImpact", () => {
  it("makes namespace cascade and storage uncertainty explicit", () => {
    const impact = resourceDeletionImpact("namespaces", {
      name: "production",
    });

    expect(impact?.scope).toBe("Namespace production");
    expect(impact?.consequences.join(" ")).toMatch(/every namespaced object/i);
    expect(impact?.recovery).toMatch(/no in-place undo/i);
    expect(impact?.recovery).toMatch(/reclaim policy/i);
  });

  it("distinguishes controller-managed and standalone pod recovery", () => {
    const impact = resourceDeletionImpact("pods", {
      namespace: "payments",
      name: "api-0",
    });

    expect(impact?.scope).toBe("Pod payments/api-0");
    expect(impact?.consequences.join(" ")).toMatch(/owning workload controller/i);
    expect(impact?.consequences.join(" ")).toMatch(/standalone pods/i);
  });

  it("warns that persistent storage recovery is policy-dependent", () => {
    const impact = resourceDeletionImpact("persistentvolumeclaims", {
      namespace: "default",
      name: "database",
    });

    expect(impact?.scope).toBe("PersistentVolumeClaim default/database");
    expect(impact?.consequences.join(" ")).toMatch(/underlying data/i);
    expect(impact?.recovery).toMatch(/snapshot/i);
  });

  it("describes authorization loss for RBAC resources", () => {
    const impact = resourceDeletionImpact("k8s-clusterrolebindings", {
      name: "platform-admins",
    });

    expect(impact?.scope).toBe("ClusterRoleBinding platform-admins");
    expect(impact?.consequences.join(" ")).toMatch(/lose API access/i);
  });

  it("returns a conservative generic preview for extension resources", () => {
    const impact = resourceDeletionImpact("widgets.example.io", {
      namespace: "default",
      name: "demo",
      kind: "Widget",
    });

    expect(impact?.scope).toBe("Widget default/demo");
    expect(impact?.consequences.join(" ")).toMatch(/owner references/i);
    expect(impact?.recovery).toMatch(/known-good manifest/i);
  });

  it("returns no preview until an object is selected", () => {
    expect(resourceDeletionImpact("pods", null)).toBeUndefined();
  });
});

describe("nodeDrainImpact", () => {
  it("previews scheduling, eviction, and disruption-policy effects", () => {
    const impact = nodeDrainImpact("worker-01");

    expect(impact?.scope).toBe("Node worker-01");
    expect(impact?.consequences.join(" ")).toMatch(/cordoned/i);
    expect(impact?.consequences.join(" ")).toMatch(/PodDisruptionBudgets/i);
    expect(impact?.recovery).toMatch(/Uncordon/i);
  });
});
