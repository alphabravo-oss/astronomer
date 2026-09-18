import { createFileRoute } from "@tanstack/react-router";
import { ConfigurationTemplatesPage } from "@/routes/dashboard/delivery/configuration-templates/index";

export const Route = createFileRoute(
  "/dashboard/clusters/$id/delivery/configuration-templates/",
)({ component: ConfigurationTemplatesPage });
