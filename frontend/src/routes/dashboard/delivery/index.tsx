import { createFileRoute } from "@tanstack/react-router";
import { DeliveryOverviewPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/")({
  component: DeliveryOverviewPage,
});
