import { createFileRoute, redirect } from "@tanstack/react-router";

/**
 * Settings is two layers deep: the settings sidebar → the page. The former
 * hub (a card grid repeating the sidebar) is gone; the index lands on
 * General. Per-page `SettingsAuthGate`s enforce the admin requirement.
 */
export const Route = createFileRoute("/dashboard/settings/")({
  beforeLoad: () => {
    throw redirect({ to: "/dashboard/settings/general", replace: true });
  },
});
