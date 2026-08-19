import { createFileRoute } from "@tanstack/react-router";
import { TargetsPage } from "@/routes/dashboard/delivery/targets/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/targets/",
)({
  component: TargetsPage,
});
