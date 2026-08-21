import { createFileRoute } from "@tanstack/react-router";
import { SourcesPage } from "@/routes/dashboard/delivery/sources/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/sources/",
)({
  component: SourcesPage,
});
