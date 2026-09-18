import { createFileRoute } from "@tanstack/react-router";
/**
 * Settings hub landing — a card grid that fans out to the per-area subpages
 * shipped in sprints 9–14. The original tabbed settings UI moved to
 * `/dashboard/settings/general/`; this page replaces the index with a
 * navigation surface so each surface gets a dedicated route and URL.
 *
 * Every link is admin-only; the per-page `SettingsAuthGate` enforces it.
 * Showing the cards to non-admins is still fine — they'll just bounce off a
 * 403 placeholder on click.
 */
import { Link as RouterLink } from "@tanstack/react-router";
import { ExtensionSlot } from "@/components/extensions/ExtensionSlot";
import { useIsSuperuser } from "@/components/settings/hooks";
import {
  SETTINGS_NAVIGATION,
  visibleSettingsNavigation,
} from "@/components/settings/settings-navigation";
import { PermissionState } from "@/components/ui/empty-state";
import { PageHeader, PageShell } from "@/components/ui/page";
import { useFeatureFlags } from "@/lib/hooks/clusters";
import { useAuthStore } from "@/lib/store";
import { canManageCharlie as hasCharlieManagement } from "@/components/charlie/admin-utils";

function SettingsHubPage() {
  // Most cards are superuser-only. Charlie is the deliberate exception: its
  // global charlie:manage grant exposes exactly that one card even while the
  // integration is disabled, because enablement begins from local settings.
  const { isSuperuser, ready } = useIsSuperuser();
  const { data: featureFlags } = useFeatureFlags();
  const user = useAuthStore((state) => state.user);
  const canManageCharlie = hasCharlieManagement(user);
  const navigationGroups = visibleSettingsNavigation(SETTINGS_NAVIGATION, {
    isSuperuser,
    canManageCharlie,
    extensionsEnabled: featureFlags?.["feature.extensions"] === true,
  });

  // While auth hydrates (!ready) render the header only — don't flash the full
  // grid to a user who will turn out to lack all administration access.
  if (!ready || (!isSuperuser && featureFlags === undefined)) {
    return (
      <PageShell>
        <PageHeader
          title="Settings"
          description="Platform configuration and administration."
        />
      </PageShell>
    );
  }

  if (!isSuperuser && !canManageCharlie) {
    return (
      <PageShell>
        <PageHeader
          title="Settings"
          description="Platform configuration and administration."
        />
        <PermissionState
          title="Administration permission required"
          description="Platform settings require superuser access, or charlie:manage for the Charlie administration surface."
        />
      </PageShell>
    );
  }

  return (
    <PageShell>
      <PageHeader
        title="Settings"
        description="Platform configuration and administration. All surfaces below are admin-only."
      />

      <div className="space-y-8">
        {navigationGroups.map((group) => (
          <section key={group.label} className="space-y-3">
            <div>
              <h2 className="text-sm font-semibold text-foreground">
                {group.label}
              </h2>
              <p className="mt-0.5 text-xs text-muted-foreground">
                {group.description}
              </p>
            </div>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
              {group.items.map((card) => {
                const Icon = card.icon;
                return (
                  <RouterLink
                    key={card.href}
                    to={card.href}
                    className="flex flex-col gap-2 rounded-lg border border-border bg-card p-4 text-left transition-colors hover:border-foreground/20 hover:bg-card/80"
                  >
                    <div className="flex items-center gap-2">
                      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-muted">
                        <Icon className="h-4 w-4 text-foreground" />
                      </div>
                      <p className="text-sm font-medium text-foreground">
                        {card.title}
                      </p>
                    </div>
                    <p className="line-clamp-2 text-xs text-muted-foreground">
                      {card.description}
                    </p>
                  </RouterLink>
                );
              })}
            </div>
          </section>
        ))}
      </div>

      {/* §HostMounts mount point 4 — enabled `settingsPage` extensions append
          here. Renders nothing when no extension declares a settings point. */}
      <ExtensionSlot
        point="settingsPage"
        className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3"
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/settings/")({
  component: SettingsHubPage,
});
