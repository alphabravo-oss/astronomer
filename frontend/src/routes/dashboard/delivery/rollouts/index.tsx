import { createFileRoute } from "@tanstack/react-router";
import { RolloutsPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/rollouts/")({
  component: RolloutsPage,
});
