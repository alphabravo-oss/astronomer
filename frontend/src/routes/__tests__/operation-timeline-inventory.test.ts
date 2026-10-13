import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

const routeRoot = join(process.cwd(), "src/routes/dashboard");

/**
 * Detail routes with server-persisted lifecycle events must expose the shared
 * timeline. This inventory is intentional: a new event-capable detail route
 * must be added here with its timeline integration in the same change.
 */
const eventCapableRoutes = [
  ["clusters/$id/apps/-page.tsx", "CatalogOperationTimeline"],
  ["delivery/rollouts/$rolloutId/-page.tsx", "RolloutEventTimeline"],
  ["delivery/rollouts/$rolloutId/-event-timeline.tsx", "OperationTimeline"],
  ["delivery/deployments/$deploymentId/-page.tsx", "DeploymentEventTimeline"],
  ["settings/backup/-destinations-section.tsx", "OperationMutationTimeline"],
] as const;

describe("operation timeline inventory", () => {
  it("hands application configuration operations to the cluster Applications timeline", () => {
    const source = readFileSync(
      join(routeRoot, "catalog/-configuration-page.tsx"),
      "utf8",
    );
    expect(source).toContain("onOperationStarted");
    expect(source).toContain("/dashboard/clusters/${clusterId}/apps?");
    expect(source).toContain("&operation=${encodeURIComponent(id)}");
  });

  it.each(eventCapableRoutes)(
    "%s renders the shared %s contract",
    (route, timeline) => {
      const source = readFileSync(join(routeRoot, route), "utf8");
      expect(source).toContain(timeline);
    },
  );
});
