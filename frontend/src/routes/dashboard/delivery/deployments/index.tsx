import { createFileRoute } from "@tanstack/react-router";
import { DeploymentsPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/deployments/")({
  component: DeploymentsPage,
});
