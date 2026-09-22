import { createFileRoute } from "@tanstack/react-router";
import { ClusterAppsPage } from "./-page";

export const Route = createFileRoute("/dashboard/clusters/$id/apps/")({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { install?: string; section?: string } & Record<string, unknown>,
  component: ClusterAppsPage,
});
