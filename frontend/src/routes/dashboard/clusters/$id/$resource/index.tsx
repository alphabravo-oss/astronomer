import { createFileRoute } from "@tanstack/react-router";

import { ClusterResourcePage } from "@/components/resources/resource-list-page";

export const Route = createFileRoute("/dashboard/clusters/$id/$resource/")({
  component: ClusterResourcePage,
});
