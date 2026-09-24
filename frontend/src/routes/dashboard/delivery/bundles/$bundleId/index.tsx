import { createFileRoute } from "@tanstack/react-router";
import { BundleDetailPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/bundles/$bundleId/")({
  component: BundleDetailPage,
});
