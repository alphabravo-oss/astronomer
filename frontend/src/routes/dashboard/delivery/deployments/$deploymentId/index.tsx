import { createFileRoute } from "@tanstack/react-router";
import { DeploymentDetailPage } from "./-page";

export const Route = createFileRoute(
  "/dashboard/delivery/deployments/$deploymentId/",
)({ component: DeploymentDetailPage });
