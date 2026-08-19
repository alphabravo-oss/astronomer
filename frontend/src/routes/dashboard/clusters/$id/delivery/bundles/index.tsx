import { createFileRoute } from "@tanstack/react-router";
import { BundlesPage } from "@/routes/dashboard/delivery/bundles/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/bundles/",
)({
  component: BundlesPage,
});
