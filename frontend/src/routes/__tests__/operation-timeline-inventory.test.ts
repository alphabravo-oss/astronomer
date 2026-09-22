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
  ["catalog/index.tsx", "CatalogOperationTimeline"],
  ["delivery/rollouts/$rolloutId/index.tsx", "OperationTimeline"],
  ["delivery/deployments/$deploymentId/index.tsx", "DeploymentEventTimeline"],
  ["settings/backup/-page.tsx", "OperationMutationTimeline"],
] as const;

describe("operation timeline inventory", () => {
  it.each(eventCapableRoutes)(
    "%s renders the shared %s contract",
    (route, timeline) => {
      const source = readFileSync(join(routeRoot, route), "utf8");
      expect(source).toContain(timeline);
    },
  );
});
