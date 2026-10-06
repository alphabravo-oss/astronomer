import { describe, expect, it } from "vitest";

import type { ResourceDiscoveryView } from "@/lib/api/resources";
import type { K8sObject } from "@/components/resources/resource-detail-model";
import {
  conditionChips,
  effectivePodStatus,
  hasGuidedEditForm,
  podContainerSummary,
  resolveManagedBy,
  resolveOwnerLink,
  resourceAge,
  splitChips,
} from "@/components/resources/resource-masthead-model";

describe("resolveOwnerLink", () => {
  const discovery = {
    clusterId: "c1",
    resources: [
      {
        kind: "ReplicaSet",
        apiGroup: "apps",
        apiVersion: "v1",
        namespaced: true,
        resourceType: "replicasets",
      },
    ],
    crds: [],
    crdContinue: "",
    partial: false,
    errors: {},
  } as unknown as ResourceDiscoveryView;
  const obj: K8sObject = {
    metadata: {
      ownerReferences: [
        { apiVersion: "v1", kind: "Node", name: "n1" },
        {
          apiVersion: "apps/v1",
          kind: "ReplicaSet",
          name: "web-abc",
          controller: true,
        },
      ],
    },
  };

  it("links the controller owner using live discovery", () => {
    expect(resolveOwnerLink(obj, "c1", "prod", discovery)).toEqual({
      kind: "ReplicaSet",
      name: "web-abc",
      href: "/dashboard/clusters/c1/replicasets/prod/web-abc",
    });
  });

  it("shows text only when discovery is unavailable", () => {
    expect(resolveOwnerLink(obj, "c1", "prod")).toEqual({
      kind: "ReplicaSet",
      name: "web-abc",
      href: undefined,
    });
  });

  it("returns undefined without owners", () => {
    expect(resolveOwnerLink({ metadata: {} }, "c1")).toBeUndefined();
  });
});

describe("resolveManagedBy", () => {
  it("detects Flux Kustomization", () => {
    expect(
      resolveManagedBy({
        metadata: {
          labels: {
            "kustomize.toolkit.fluxcd.io/name": "apps",
            "kustomize.toolkit.fluxcd.io/namespace": "flux-system",
          },
        },
      }),
    ).toMatchObject({
      source: "flux-kustomization",
      detail: "flux-system/apps",
    });
  });

  it("detects Helm release from annotations", () => {
    expect(
      resolveManagedBy({
        metadata: {
          labels: { "app.kubernetes.io/managed-by": "Helm" },
          annotations: {
            "meta.helm.sh/release-name": "redis",
            "meta.helm.sh/release-namespace": "data",
          },
        },
      }),
    ).toMatchObject({ source: "helm", detail: "data/redis" });
  });

  it("does not invent ownership", () => {
    expect(
      resolveManagedBy({ metadata: { labels: { app: "web" } } }),
    ).toBeUndefined();
  });
});

describe("conditionChips", () => {
  it("colors by polarity and carries reason and message", () => {
    const chips = conditionChips([
      { type: "Ready", status: "True" },
      { type: "MemoryPressure", status: "False" },
      { type: "DiskPressure", status: "True", reason: "Low", message: "full" },
      { type: "Progressing", status: "Unknown" },
      { status: "True" },
    ]);
    expect(chips.map((c) => c.tone)).toEqual(["ok", "ok", "bad", "unknown"]);
    expect(chips[2].detail).toBe("Low: full");
  });
});

describe("podContainerSummary", () => {
  it("counts ready/total, restarts and the latest termination", () => {
    const summary = podContainerSummary({
      status: {
        containerStatuses: [
          {
            ready: true,
            restartCount: 1,
            lastState: {
              terminated: {
                reason: "Error",
                exitCode: 1,
                finishedAt: "2026-01-01T00:00:00Z",
              },
            },
          },
          {
            ready: false,
            restartCount: 2,
            lastState: {
              terminated: {
                reason: "OOMKilled",
                exitCode: 137,
                finishedAt: "2026-02-01T00:00:00Z",
              },
            },
          },
        ],
      },
    });
    expect(summary).toEqual({
      ready: 1,
      total: 2,
      restarts: 3,
      lastTermination: "OOMKilled (exit 137)",
    });
  });

  it("is undefined before containers report", () => {
    expect(podContainerSummary({ status: {} })).toBeUndefined();
  });
});

describe("effectivePodStatus", () => {
  it("prefers waiting reason over phase", () => {
    expect(
      effectivePodStatus({
        status: {
          phase: "Running",
          containerStatuses: [
            { state: { waiting: { reason: "CrashLoopBackOff" } } },
          ],
        },
      }),
    ).toBe("CrashLoopBackOff");
    expect(effectivePodStatus({ status: { phase: "Pending" } })).toBe(
      "Pending",
    );
  });
});

describe("misc helpers", () => {
  it("formats age with an exact timestamp", () => {
    const age = resourceAge("2026-01-01T00:00:00Z");
    expect(age?.exact).toMatch(/2026|2025/);
    expect(resourceAge(undefined)).toBeUndefined();
  });

  it("collapses chips after the limit", () => {
    const items = Array.from({ length: 9 }, (_, i) => i);
    expect(splitChips(items, false)).toEqual({
      visible: items.slice(0, 6),
      hidden: 3,
    });
    expect(splitChips(items, true).hidden).toBe(0);
    expect(splitChips([1, 2], false).hidden).toBe(0);
  });

  it("knows which kinds have a guided form", () => {
    expect(hasGuidedEditForm("Deployment")).toBe(true);
    expect(hasGuidedEditForm("Service")).toBe(true);
    expect(hasGuidedEditForm("Pod")).toBe(false);
    expect(hasGuidedEditForm(undefined)).toBe(false);
  });
});
