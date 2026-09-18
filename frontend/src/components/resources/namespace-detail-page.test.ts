import { describe, expect, it } from "vitest";

import { buildGenericNamespaceRows } from "@/components/resources/namespace-detail-page";
import type { GenericK8sResource } from "@/types";

const resource = (name: string, namespace: string): GenericK8sResource => ({
  name,
  namespace,
  clusterId: "cluster-1",
  labels: {},
  annotations: {},
  createdAt: "2026-08-25T20:00:00Z",
});

describe("namespace resource aggregation", () => {
  it("keeps only resources in the selected namespace and builds detail links", () => {
    const rows = buildGenericNamespaceRows(
      "cluster-1",
      "astronomer",
      "configmaps",
      "ConfigMap",
      [resource("settings", "astronomer"), resource("settings", "default")],
    );

    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({
      id: "ConfigMap/settings",
      kind: "ConfigMap",
      name: "settings",
      href: "/dashboard/clusters/cluster-1/configmaps/astronomer/settings",
    });
  });
});
