import { describe, expect, it } from "vitest";
import {
  REVISION_ANNOTATION,
  ownedReplicaSets,
} from "@/components/resources/deployment-replicasets";
import type { GenericK8sResource } from "@/types/generic-kubernetes";

function rs(
  name: string,
  hash: string | undefined,
  revision: number | undefined,
  desired: number,
  createdAt = "2026-10-01T00:00:00Z",
): GenericK8sResource {
  return {
    name,
    namespace: "default",
    clusterId: "c1",
    labels: hash ? { "pod-template-hash": hash } : {},
    annotations: revision ? { [REVISION_ANNOTATION]: String(revision) } : {},
    createdAt,
    desired,
    ready: desired,
    available: desired,
  };
}

describe("ownedReplicaSets", () => {
  it("keeps only ReplicaSets named exactly <deployment>-<hash> with that hash label", () => {
    const sets = ownedReplicaSets(
      [
        rs("web-7d4b9c", "7d4b9c", 2, 3),
        rs("web-5f6a8d", "5f6a8d", 1, 0),
        rs("web-admin-77aa11", "77aa11", 4, 2), // another deployment, same prefix
        rs("web-nohash", undefined, 3, 1),
        rs("api-7d4b9c", "7d4b9c", 2, 3),
      ],
      "web",
    );
    expect(sets.map((s) => s.name)).toEqual(["web-7d4b9c", "web-5f6a8d"]);
  });

  it("orders newest revision first and marks the live set current", () => {
    const sets = ownedReplicaSets(
      [
        rs("web-aaa111", "aaa111", 1, 0),
        rs("web-ccc333", "ccc333", 3, 2),
        rs("web-bbb222", "bbb222", 2, 1),
      ],
      "web",
    );
    expect(sets.map((s) => [s.name, s.revision, s.current])).toEqual([
      ["web-ccc333", 3, true],
      ["web-bbb222", 2, false],
      ["web-aaa111", 1, false],
    ]);
  });

  it("treats a missing or invalid revision as null and sorts it last", () => {
    const sets = ownedReplicaSets(
      [
        rs("web-aaa111", "aaa111", undefined, 0),
        rs("web-bbb222", "bbb222", 2, 1),
      ],
      "web",
    );
    expect(sets.map((s) => s.revision)).toEqual([2, null]);
  });

  it("marks nothing current when every set is scaled to zero", () => {
    const sets = ownedReplicaSets([rs("web-aaa111", "aaa111", 1, 0)], "web");
    expect(sets[0].current).toBe(false);
  });
});
