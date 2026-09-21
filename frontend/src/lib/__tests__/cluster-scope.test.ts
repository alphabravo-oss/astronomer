import {
  MAX_RECENT_CLUSTERS,
  useClusterScopeStore,
  withRecentCluster,
} from "@/lib/cluster-scope";

describe("withRecentCluster", () => {
  it("orders most-recent-first and de-dupes a repeat visit", () => {
    let recent: string[] = [];
    recent = withRecentCluster(recent, "a");
    recent = withRecentCluster(recent, "b");
    recent = withRecentCluster(recent, "c");
    expect(recent).toEqual(["c", "b", "a"]);

    // Revisiting "a" moves it back to the front without duplicating it.
    recent = withRecentCluster(recent, "a");
    expect(recent).toEqual(["a", "c", "b"]);
  });

  it("caps at MAX_RECENT_CLUSTERS", () => {
    let recent: string[] = [];
    for (let i = 0; i < MAX_RECENT_CLUSTERS + 3; i++) {
      recent = withRecentCluster(recent, `cluster-${i}`);
    }
    expect(recent).toHaveLength(MAX_RECENT_CLUSTERS);
    expect(recent[0]).toBe(`cluster-${MAX_RECENT_CLUSTERS + 2}`);
  });
});

describe("useClusterScopeStore recentClusterIds", () => {
  beforeEach(() => {
    useClusterScopeStore.setState({
      lastClusterId: null,
      namespacesByCluster: {},
      projectByCluster: {},
      recentClusterIds: [],
    });
  });

  it("records visited clusters most-recent-first via setClusterScope", () => {
    const { setClusterScope } = useClusterScopeStore.getState();
    setClusterScope("cluster-a", null, null);
    setClusterScope("cluster-b", null, null);
    setClusterScope("cluster-c", null, null);

    expect(useClusterScopeStore.getState().recentClusterIds).toEqual([
      "cluster-c",
      "cluster-b",
      "cluster-a",
    ]);
  });
});
