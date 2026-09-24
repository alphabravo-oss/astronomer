import { createFileRoute } from "@tanstack/react-router";
import { DeploymentsPage } from "@/routes/dashboard/delivery/deployments/-page";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/deployments/",
)({
  component: DeploymentsPage,
});
