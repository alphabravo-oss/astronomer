import { ChevronDown, SlidersHorizontal } from "lucide-react";

import { useLocation } from "@tanstack/react-router";
import { canManageCharlie as hasCharlieManagement } from "@/components/charlie/admin-utils";
import { useIsSuperuser } from "@/components/settings/hooks";
import {
  SETTINGS_NAVIGATION,
  settingsItemIsActive,
  visibleSettingsNavigation,
} from "@/components/settings/settings-navigation";
import { useFeatureFlags } from "@/lib/hooks/clusters";
import { useAuthStore } from "@/lib/store";
import { SidebarGroup, SidebarItem } from "@/ui";

function NavigationGroups() {
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
    <div className="space-y-2">
      {groups.map((group) => (
        <SidebarGroup key={group.label} label={group.label}>
          {group.items.map((item) => (
            <SidebarItem
              key={item.href}
              to={item.href}
              icon={item.icon}
              label={item.title}
              active={settingsItemIsActive(pathname, item.href)}
            />
          ))}
        </SidebarGroup>
      ))}
    </div>
  );
}

/** Second layer of Settings: a persistent page list (sentence-case groups). */
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
          className="sticky top-6 max-h-[calc(100vh-8rem)] overflow-y-auto pl-2 pr-2"
        >
          <NavigationGroups />
        </nav>
      </aside>
    </>
  );
}
