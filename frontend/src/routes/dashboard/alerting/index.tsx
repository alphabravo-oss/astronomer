import { createFileRoute } from "@tanstack/react-router";
import { AlertingPage } from "./-page";

export const Route = createFileRoute("/dashboard/alerting/")({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: AlertingPage,
});
