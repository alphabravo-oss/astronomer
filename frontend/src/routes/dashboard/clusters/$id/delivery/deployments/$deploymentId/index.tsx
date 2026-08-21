import { createFileRoute } from "@tanstack/react-router";
import { DeploymentDetailPage } from "@/routes/dashboard/delivery/deployments/$deploymentId/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/deployments/$deploymentId/",
)({
  component: DeploymentDetailPage,
});
