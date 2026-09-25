import { describe, expect, it } from "vitest";
import { clusterScopeApplicability } from "./cluster-scope-applicability";

const cluster = "/dashboard/clusters/cluster-1";

describe("cluster scope applicability", () => {
  it.each(["metrics", "nodes", "monitoring-stack", "snapshots", "tools"])(
    "hides misleading scope controls on cluster-wide %s pages",
    (page) => {
      expect(clusterScopeApplicability(`${cluster}/${page}`)).toEqual({
        project: false,
        namespaces: false,
      });
    },
  );

  it.each(["pods", "deployments", "services", "events", "workloads"])(
    "shows project and namespace controls for %s inventory",
    (page) => {
      expect(clusterScopeApplicability(`${cluster}/${page}`)).toEqual({
        project: true,
        namespaces: true,
      });
    },
  );

  it("uses one project scope for cluster delivery without a namespace control", () => {
    expect(clusterScopeApplicability(`${cluster}/delivery/targets`)).toEqual({
      project: true,
      namespaces: false,
    });
  });

  it("hides Apps scope where repositories are fleet-wide", () => {
    expect(
      clusterScopeApplicability(
        `${cluster}/apps`,
        "?section=repositories&project=remembered",
      ),
    ).toEqual({ project: false, namespaces: false });
  });

  it("does not show namespace scope on the CRD discovery landing page", () => {
    expect(clusterScopeApplicability(`${cluster}/custom-resources`)).toEqual({
      project: false,
      namespaces: false,
    });
    expect(
      clusterScopeApplicability(
        `${cluster}/custom-resources/apps/v1/deployments`,
      ),
    ).toEqual({ project: true, namespaces: true });
  });
});
