import { createFileRoute } from "@tanstack/react-router";
import { DeploymentsPage } from "@/routes/dashboard/delivery/deployments/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/deployments/",
)({
  component: DeploymentsPage,
});
