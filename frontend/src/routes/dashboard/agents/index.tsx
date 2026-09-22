import { createFileRoute } from "@tanstack/react-router";
import { ClusterAgentsPage } from "./-page";

export const Route = createFileRoute("/dashboard/agents/")({
  validateSearch: (search: Record<string, unknown>) =>
    search as { q?: string } & Record<string, unknown>,
  component: ClusterAgentsPage,
});
