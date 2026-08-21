import { createFileRoute } from "@tanstack/react-router";
import { RolloutDetailPage } from "@/routes/dashboard/delivery/rollouts/$rolloutId/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/rollouts/$rolloutId/",
)({
  component: RolloutDetailPage,
});
