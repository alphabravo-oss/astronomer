import { createFileRoute, redirect } from "@tanstack/react-router";

// Moved under Observability (P-018 step 9): shared Thanos/Alertmanager
// lifecycle is surfaced next to Metrics/Alerting/Logging, not buried in the
// superuser-only settings hub. Kept as a redirect so old bookmarks resolve.
export const Route = createFileRoute("/dashboard/settings/monitoring/")({
  beforeLoad: () => {
    throw redirect({ to: "/dashboard/monitoring/stacks" });
  },
});
