import {
  rolloutHistoryEntries,
  rolloutSourceForKind,
  supportsRolloutHistory,
} from "./rollout-history";

describe("rolloutSourceForKind", () => {
  it("uses native revision resources for supported controllers", () => {
    expect(rolloutSourceForKind("Deployment", "apps")).toEqual({
      path: "apis/apps/v1/namespaces/apps/replicasets",
      resourceType: "replicasets",
    });
    expect(rolloutSourceForKind("StatefulSet", "apps")?.path).toBe(
      "apis/apps/v1/namespaces/apps/controllerrevisions",
    );
    expect(rolloutSourceForKind("CronJob", "batch")).toEqual({
      path: "apis/batch/v1/namespaces/batch/jobs",
      resourceType: "jobs",
    });
    expect(rolloutSourceForKind("Pod", "apps")).toBeUndefined();
  });

  it("advertises rollout history only for kinds with a native source", () => {
    expect(supportsRolloutHistory("Deployment")).toBe(true);
    expect(supportsRolloutHistory("DaemonSet")).toBe(true);
    expect(supportsRolloutHistory("CronJob")).toBe(true);
    expect(supportsRolloutHistory("Service")).toBe(false);
  });
});

describe("rolloutHistoryEntries", () => {
  it("filters by owner UID, sorts newest revision first, and preserves change cause", () => {
    const entries = rolloutHistoryEntries(
      {
        items: [
          {
            metadata: {
              name: "web-aaa",
              uid: "rs-1",
              creationTimestamp: "2026-01-01T00:00:00Z",
              ownerReferences: [
                { kind: "Deployment", name: "web", uid: "deployment-1" },
              ],
              annotations: {
                "deployment.kubernetes.io/revision": "1",
                "kubernetes.io/change-cause": "initial release",
              },
            },
            status: { replicas: 3, readyReplicas: 3 },
          },
          {
            metadata: {
              name: "unrelated",
              ownerReferences: [
                { kind: "Deployment", name: "other", uid: "deployment-2" },
              ],
              annotations: { "deployment.kubernetes.io/revision": "99" },
            },
          },
          {
            metadata: {
              name: "web-bbb",
              uid: "rs-2",
              creationTimestamp: "2026-01-02T00:00:00Z",
              ownerReferences: [
                { kind: "Deployment", name: "web", uid: "deployment-1" },
              ],
              annotations: { "deployment.kubernetes.io/revision": "2" },
            },
            status: { replicas: 3, readyReplicas: 2 },
          },
        ],
      },
      { kind: "Deployment", name: "web", uid: "deployment-1" },
      "replicasets",
    );

    expect(entries).toHaveLength(2);
    expect(entries.map((entry) => entry.name)).toEqual([
      "web-bbb",
      "web-aaa",
    ]);
    expect(entries[0]).toMatchObject({
      revision: 2,
      status: "2/3 ready",
      resourceType: "replicasets",
    });
    expect(entries[1].changeCause).toBe("initial release");
  });

  it("supports ControllerRevision and CronJob Job status shapes", () => {
    const controller = rolloutHistoryEntries(
      {
        items: [
          {
            metadata: {
              name: "db-9",
              ownerReferences: [{ kind: "StatefulSet", name: "db" }],
            },
            revision: 9,
          },
        ],
      },
      { kind: "StatefulSet", name: "db" },
    );
    const jobs = rolloutHistoryEntries(
      {
        items: [
          {
            metadata: {
              name: "backup-123",
              ownerReferences: [{ kind: "CronJob", name: "backup" }],
            },
            status: { succeeded: 1 },
          },
        ],
      },
      { kind: "CronJob", name: "backup" },
      "jobs",
    );

    expect(controller[0]).toMatchObject({ revision: 9, status: "Recorded" });
    expect(jobs[0]).toMatchObject({ status: "1 succeeded" });
  });
});
