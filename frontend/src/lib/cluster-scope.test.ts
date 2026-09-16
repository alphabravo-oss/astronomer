import {
  canonicalNamespaces,
  parseNamespaceSelection,
  withClusterScopeSelection,
} from "@/lib/cluster-scope";

describe("cluster namespace scope URL contract", () => {
  it("canonicalizes namespace selections for stable URLs and persistence", () => {
    expect(canonicalNamespaces([" payments ", "api", "payments", ""])).toEqual([
      "api",
      "payments",
    ]);
  });

  it("distinguishes all namespaces from an explicit empty allow-list", () => {
    expect(
      parseNamespaceSelection(new URLSearchParams("tab=pods")),
    ).toBeUndefined();
    expect(parseNamespaceSelection(new URLSearchParams("namespaces="))).toEqual(
      [],
    );
  });

  it("preserves unrelated search state when changing namespace scope", () => {
    const href = withClusterScopeSelection(
      "/dashboard/clusters/c1/pods",
      new URLSearchParams("tab=events"),
      ["payments", "api"],
      null,
    );
    expect(href).toBe(
      "/dashboard/clusters/c1/pods?tab=events&namespaces=api%2Cpayments",
    );
    expect(
      withClusterScopeSelection(
        "/dashboard/clusters/c1/pods",
        new URLSearchParams(href.split("?")[1]),
        null,
        null,
      ),
    ).toBe("/dashboard/clusters/c1/pods?tab=events");
  });

  it("writes project and namespace scope atomically and removes stale scope", () => {
    expect(
      withClusterScopeSelection(
        "/dashboard/clusters/c1/pods",
        new URLSearchParams("tab=events"),
        ["payments", "api"],
        "project-1",
      ),
    ).toBe(
      "/dashboard/clusters/c1/pods?tab=events&namespaces=api%2Cpayments&project=project-1",
    );
    expect(
      withClusterScopeSelection(
        "/dashboard/clusters/c1/pods",
        new URLSearchParams(
          "tab=events&namespaces=api%2Cpayments&project=project-1",
        ),
        null,
        null,
      ),
    ).toBe("/dashboard/clusters/c1/pods?tab=events");
  });
});
