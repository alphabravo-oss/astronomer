import { ChevronDown, SlidersHorizontal } from "lucide-react";

import { Link as RouterLink } from "@tanstack/react-router";
import { useLocation } from "@tanstack/react-router";
import { cn } from "@/lib/utils";
import { canManageCharlie as hasCharlieManagement } from "@/components/charlie/admin-utils";
import { useIsSuperuser } from "@/components/settings/hooks";
import {
  SETTINGS_NAVIGATION,
  settingsItemIsActive,
  visibleSettingsNavigation,
} from "@/components/settings/settings-navigation";
import { useFeatureFlags } from "@/lib/hooks/clusters";
import { useAuthStore } from "@/lib/store";

function NavigationGroups({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = useLocation({ select: (location) => location.pathname });
  const user = useAuthStore((state) => state.user);
  const { isSuperuser } = useIsSuperuser();
  const { data: featureFlags } = useFeatureFlags();
  const groups = visibleSettingsNavigation(SETTINGS_NAVIGATION, {
    isSuperuser,
    canManageCharlie: hasCharlieManagement(user),
    extensionsEnabled: featureFlags?.["feature.extensions"] === true,
  });

  return (
    <div className="space-y-5">
      {groups.map((group) => (
        <section key={group.label} aria-labelledby={`settings-${group.label}`}>
          <h2
            id={`settings-${group.label}`}
            className="mb-1 px-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground"
          >
            {group.label}
          </h2>
          <div className="space-y-0.5">
            {group.items.map((item) => {
              const Icon = item.icon;
              const active = settingsItemIsActive(pathname, item.href);
              return (
                <RouterLink
                  key={item.href}
                  to={item.href}
                  onClick={onNavigate}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "flex min-h-9 items-center gap-2 rounded-md px-2 text-sm transition-colors",
                    active
                      ? "bg-accent font-medium text-foreground"
                      : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                  )}
                >
                  <Icon className="h-4 w-4 shrink-0" />
                  <span>{item.title}</span>
                </RouterLink>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}

export function SettingsSubnavigation() {
  return (
    <>
      <details className="group rounded-lg border border-border bg-card lg:hidden">
        <summary className="flex min-h-11 cursor-pointer list-none items-center gap-2 px-3 text-sm font-medium text-foreground [&::-webkit-details-marker]:hidden">
          <SlidersHorizontal className="h-4 w-4" />
          Settings navigation
          <ChevronDown className="ml-auto h-4 w-4 transition-transform group-open:rotate-180" />
        </summary>
        <nav aria-label="Settings" className="border-t border-border p-3">
          <NavigationGroups />
        </nav>
      </details>

      <aside className="hidden lg:block">
        <nav
          aria-label="Settings"
          className="sticky top-6 max-h-[calc(100vh-8rem)] overflow-y-auto pr-2"
        >
          <NavigationGroups />
        </nav>
      </aside>
    </>
  );
}
