import { createFileRoute } from "@tanstack/react-router";
import { TargetDetailPage } from "@/routes/dashboard/delivery/targets/$targetId/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/targets/$targetId/",
)({
  component: TargetDetailPage,
});
