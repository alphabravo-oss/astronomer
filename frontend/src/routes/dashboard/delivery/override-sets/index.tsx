import { createFileRoute } from "@tanstack/react-router";
import { OverrideSetsPage } from "./-page";

export const Route = createFileRoute("/dashboard/delivery/override-sets/")({
  component: OverrideSetsPage,
});
