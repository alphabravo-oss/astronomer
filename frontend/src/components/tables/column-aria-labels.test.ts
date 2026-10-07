import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * Headers shortened during plan 031 Phase 6b must keep their full name as the
 * accessible name (`ariaLabel`). Source-level so no table has to be rendered.
 */
const SHORT_HEADERS: Record<string, string[]> = {
  "components/clusters/control-plane-snapshots-page.tsx": ["etcd rev"],
  "components/clusters/snapshot-tables.tsx": ["Warn / Err"],
  "components/resources/resource-generic-columns.tsx": [
    "Last Run",
    "Min Avail",
    "Max Unavail",
  ],
  "components/resources/resource-network-storage-columns.tsx": ["Access"],
  "routes/dashboard/clusters/-columns.tsx": ["Platform", "K8s", "Seen"],
  "routes/dashboard/clusters/$id/nodes/$nodeName/-node-workload-tabs.tsx": [
    "Heartbeat",
    "Transition",
  ],
  "routes/dashboard/clusters/$id/delivery/system-components/$componentId/-columns.tsx":
    ["Req / capacity"],
  "routes/dashboard/clusters/$id/delivery/system-components/-columns.tsx": [
    "Ready",
  ],
  "routes/dashboard/cluster-templates/index.tsx": ["Clusters"],
  "routes/dashboard/alerting/baselines/index.tsx": ["Last"],
  "routes/dashboard/agents/-columns.tsx": ["Heartbeat"],
  "routes/dashboard/delivery/-estate-columns.tsx": ["Heartbeat"],
  "routes/dashboard/settings/quotas/index.tsx": ["Flagged", "Worst", "Caps"],
  "routes/dashboard/logging/-operations-tab.tsx": ["Updated"],
  "routes/dashboard/delivery/deployments/-page.tsx": ["Observed"],
  "routes/dashboard/delivery/deployments/$deploymentId/-page.tsx": ["Changed"],
  "routes/dashboard/delivery/rollouts/-page.tsx": ["Version"],
  "routes/dashboard/delivery/targets/-page.tsx": ["Version"],
  "routes/dashboard/delivery/sources/-page.tsx": ["Checked"],
  "routes/dashboard/projects/index.tsx": ["Quota"],
  "routes/dashboard/security/-policies-tab.tsx": ["Sync"],
  "routes/dashboard/catalog/-repositories-table.tsx": ["Synced"],
  "routes/dashboard/catalog/-installed-tab.tsx": ["Version", "Age"],
};

describe("shortened column headers keep a full accessible name", () => {
  for (const [file, headers] of Object.entries(SHORT_HEADERS)) {
    it(file, () => {
      const src = readFileSync(resolve(__dirname, "../..", file), "utf8");
      for (const h of headers) {
        const re = new RegExp(
          `header: "${h.replace(/[/.]/g, "\\$&")}",\\s*\\n\\s*ariaLabel: "[^"]+",`,
        );
        expect(src, `${h} in ${file}`).toMatch(re);
      }
    });
  }
});
