import { describe, expect, it } from "vitest";
import { resolveColumnLayout } from "@/components/ui/data-table-layout";
import type { Column } from "@/components/ui/data-table";
import { estateClusterColumns } from "@/components/clusters/estate-clusters-table";
import { workloadColumns } from "@/components/clusters/workloads-table";
import { clusterColumns } from "@/routes/dashboard/clusters/-columns";
import { agentColumns } from "@/routes/dashboard/agents/-columns";
import { clusterMetricColumns } from "@/components/monitoring/cluster-metric-columns";
import { systemComponentColumns } from "@/routes/dashboard/clusters/$id/delivery/system-components/-columns";
import {
  systemResourceColumns,
  systemVolumeColumns,
} from "@/routes/dashboard/clusters/$id/delivery/system-components/$componentId/-columns";
import { genericColumnMap } from "@/components/resources/resource-generic-columns";

// Plan 031 P6b slice 1: every column has a kind (or explicit size) and
// exactly one column per table grows.
const noop = () => undefined;
const tables: Record<string, Column<never>[]> = {
  estate: estateClusterColumns as Column<never>[],
  workloads: workloadColumns("c") as Column<never>[],
  clusters: clusterColumns(noop as never, noop, noop) as Column<never>[],
  agents: agentColumns(noop, noop) as Column<never>[],
  metricsNodes: clusterMetricColumns("c").nodeColumns as Column<never>[],
  metricsNamespaces: clusterMetricColumns("c").nsColumns as Column<never>[],
  systemComponents: systemComponentColumns("c") as Column<never>[],
  systemVolumes: systemVolumeColumns("c") as Column<never>[],
  systemResources: systemResourceColumns("c") as Column<never>[],
  ...Object.fromEntries(
    [
      "jobs",
      "cronjobs",
      "replicasets",
      "hpa",
      "poddisruptionbudgets",
      "resourcequotas",
      "limitranges",
    ].map((key) => [
      `generic:${key}`,
      (genericColumnMap[key] ?? []) as Column<never>[],
    ]),
  ),
};

describe("slice 1 column layouts", () => {
  for (const [name, columns] of Object.entries(tables)) {
    it(`${name}: all columns sized, one grow column`, () => {
      expect(columns.length).toBeGreaterThan(0);
      const layouts = columns.map((column) => resolveColumnLayout(column));
      for (const [index, layout] of layouts.entries()) {
        expect(layout.sized, `${name}.${columns[index].key}`).toBe(true);
      }
      expect(layouts.filter((layout) => layout.grow)).toHaveLength(1);
    });
  }
});
