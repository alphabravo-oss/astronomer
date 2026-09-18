import { createFileRoute } from "@tanstack/react-router";
import { ClusterAppsPage } from "../index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/apps/repositories/",
)({
  component: () => <ClusterAppsPage initialSection="repositories" />,
});
