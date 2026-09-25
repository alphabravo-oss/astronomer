import { createFileRoute } from "@tanstack/react-router";
import { TargetDetailPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/targets/$targetId/")({
  component: TargetDetailPage,
});
