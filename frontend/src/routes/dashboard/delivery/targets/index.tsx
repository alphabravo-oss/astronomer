import { createFileRoute } from "@tanstack/react-router";
import { TargetsPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/targets/")({
  component: TargetsPage,
});
