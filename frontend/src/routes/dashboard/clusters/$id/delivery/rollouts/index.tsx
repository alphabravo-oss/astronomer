import { createFileRoute } from "@tanstack/react-router";
import { RolloutsPage } from "@/routes/dashboard/delivery/rollouts/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/rollouts/",
)({
  component: RolloutsPage,
});
