import { createFileRoute } from "@tanstack/react-router";
import { ClusterShellDeepLink } from "./-page";

export const Route = createFileRoute("/dashboard/clusters/$id/shell/")({
  component: ClusterShellDeepLink,
});
