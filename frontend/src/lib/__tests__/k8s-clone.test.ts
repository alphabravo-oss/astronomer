import { describe, expect, it } from "vitest";
import { prepareCloneManifest } from "@/lib/k8s-clone";

describe("prepareCloneManifest", () => {
  it("removes allocated Service ports and addresses without mutating the source", () => {
    const source = {
      kind: "Service",
      metadata: {
        name: "api",
        finalizers: ["cleanup"],
        annotations: {
          "kubectl.kubernetes.io/last-applied-configuration":
            "sensitive snapshot",
          team: "ops",
        },
      },
      spec: {
        clusterIP: "10.0.0.1",
        clusterIPs: ["10.0.0.1"],
        healthCheckNodePort: 30000,
        ports: [{ port: 80, nodePort: 30001 }],
      },
    };
    const clone = prepareCloneManifest(source);
    expect(clone.spec).toEqual({ ports: [{ port: 80 }] });
    expect(clone.metadata).toEqual({
      name: "api-copy",
      annotations: { team: "ops" },
    });
    expect(source.spec.ports[0].nodePort).toBe(30001);
  });
  it("preserves headless Service identity", () => {
    expect(
      prepareCloneManifest({
        kind: "Service",
        spec: { clusterIP: "None", clusterIPs: ["None"] },
      }).spec,
    ).toEqual({ clusterIP: "None", clusterIPs: ["None"] });
  });
  it("does not clone PVC binding or selected-node annotations", () => {
    const clone = prepareCloneManifest({
      kind: "PersistentVolumeClaim",
      metadata: {
        name: "disk",
        annotations: {
          "pv.kubernetes.io/bind-completed": "yes",
          "volume.kubernetes.io/selected-node": "node-a",
          team: "ops",
        },
      },
      spec: {
        volumeName: "existing-pv",
        resources: { requests: { storage: "1Gi" } },
      },
    });
    expect(clone.spec).toEqual({ resources: { requests: { storage: "1Gi" } } });
    expect(clone.metadata).toEqual({
      name: "disk-copy",
      annotations: { team: "ops" },
    });
  });
  it("strips every server-managed metadata key", () => {
    const source = {
      apiVersion: "apps/v1",
      kind: "Deployment",
      metadata: {
        name: "api",
        namespace: "production",
        uid: "abc-123",
        resourceVersion: "9876",
        creationTimestamp: "2026-01-01T00:00:00Z",
        generation: 4,
        managedFields: [{ manager: "kubectl" }],
        ownerReferences: [{ kind: "ReplicaSet", name: "api-abc" }],
        selfLink: "/apis/apps/v1/namespaces/production/deployments/api",
        labels: { app: "api" },
      },
      spec: { replicas: 3 },
      status: { readyReplicas: 3 },
    };

    const clone = prepareCloneManifest(source);
    const metadata = clone.metadata as Record<string, unknown>;

    expect(metadata.uid).toBeUndefined();
    expect(metadata.resourceVersion).toBeUndefined();
    expect(metadata.creationTimestamp).toBeUndefined();
    expect(metadata.generation).toBeUndefined();
    expect(metadata.managedFields).toBeUndefined();
    expect(metadata.ownerReferences).toBeUndefined();
    expect(metadata.selfLink).toBeUndefined();
    expect(clone.status).toBeUndefined();

    // Untouched fields survive.
    expect(metadata.namespace).toBe("production");
    expect(metadata.labels).toEqual({ app: "api" });
    expect(clone.spec).toEqual({ replicas: 3 });
    expect(clone.apiVersion).toBe("apps/v1");
    expect(clone.kind).toBe("Deployment");
  });

  it("renames metadata.name to <name>-copy", () => {
    const clone = prepareCloneManifest({
      metadata: { name: "api" },
    });
    expect((clone.metadata as Record<string, unknown>).name).toBe("api-copy");
  });

  it("tolerates a missing metadata.name", () => {
    const clone = prepareCloneManifest({ metadata: {} });
    expect((clone.metadata as Record<string, unknown>).name).toBeUndefined();
  });

  it("tolerates a missing metadata block entirely", () => {
    const clone = prepareCloneManifest({ kind: "ConfigMap" });
    expect(clone.metadata).toEqual({});
  });

  it("does not mutate the source object", () => {
    const source = {
      metadata: { name: "api", uid: "abc" },
      status: { ok: true },
    };
    prepareCloneManifest(source);
    expect(source.metadata.uid).toBe("abc");
    expect(source.status).toEqual({ ok: true });
  });
});
