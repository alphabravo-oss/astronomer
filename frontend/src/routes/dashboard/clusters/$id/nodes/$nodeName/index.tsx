import { createFileRoute, lazyRouteComponent } from "@tanstack/react-router";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/nodes/$nodeName/",
)({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: lazyRouteComponent(() => import("./-page"), "NodeDetailPage"),
});
