import { createFileRoute, Outlet } from "@tanstack/react-router";

import { SettingsSubnavigation } from "@/components/settings/settings-subnavigation";

function SettingsLayout() {
  return (
    <div className="grid gap-6 lg:grid-cols-[14rem_minmax(0,1fr)] lg:items-start">
      <SettingsSubnavigation />
      <div className="min-w-0">
        <Outlet />
      </div>
    </div>
  );
}

export const Route = createFileRoute("/dashboard/settings")({
  component: SettingsLayout,
});
