import { createFileRoute } from "@tanstack/react-router";
import { ClusterDetailPage } from "./-page";

export const Route = createFileRoute("/dashboard/clusters/$id/")({
  component: ClusterDetailPage,
});
