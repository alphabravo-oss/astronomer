import { createFileRoute } from "@tanstack/react-router";
import { OverrideSetsPage } from "@/routes/dashboard/delivery/override-sets/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/override-sets/",
)({ component: OverrideSetsPage });
