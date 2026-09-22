import { createFileRoute } from "@tanstack/react-router";
import { ConfigurationTemplatesPage } from "./-page";

export const Route = createFileRoute(
  "/dashboard/delivery/configuration-templates/",
)({ component: ConfigurationTemplatesPage });
