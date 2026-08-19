import { createFileRoute } from "@tanstack/react-router";
import { BundleDetailPage } from "@/routes/dashboard/delivery/bundles/$bundleId/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/bundles/$bundleId/",
)({
  component: BundleDetailPage,
});
