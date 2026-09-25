import { createFileRoute } from "@tanstack/react-router";
import { RolloutDetailPage } from "./-page";

export const Route = createFileRoute(
  "/dashboard/delivery/rollouts/$rolloutId/",
)({ component: RolloutDetailPage });
