import { createFileRoute } from "@tanstack/react-router";
import { BundlesPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/bundles/")({
  component: BundlesPage,
});
