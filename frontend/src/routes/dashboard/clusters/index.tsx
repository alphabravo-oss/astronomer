import { createFileRoute } from "@tanstack/react-router";
import { ClustersPage } from "./-page";

export const Route = createFileRoute("/dashboard/clusters/")({
  // Deep-link contract (P2.4): typed passthrough — unrelated params survive.
  validateSearch: (search: Record<string, unknown>) =>
    search as { register?: string; status?: string; q?: string } & Record<
      string,
      unknown
    >,
  component: ClustersPage,
});
