import { createFileRoute } from "@tanstack/react-router";

import { SharedMonitoringStacksPage } from "@/components/monitoring/shared-stacks-page";

export const Route = createFileRoute("/dashboard/monitoring/stacks/")({
  component: SharedMonitoringStacksPage,
});
