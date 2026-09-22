import {
  filterNavGroups,
  globalNavGroups,
  type NavItem,
  type NavGroup,
} from "@/components/layout/sidebar-navigation";
import type { FeatureFlags } from "@/lib/api/feature-flags";
import type { User } from "@/types";
import { navGroupItems } from "./nav-group-items";
import {
  SETTINGS_NAVIGATION,
  visibleSettingsNavigation,
} from "@/components/settings/settings-navigation";
import { isSuperuser } from "@/lib/permissions";
import { canManageCharlie } from "@/components/charlie/admin-utils";

export function commandPaletteSettings(
  user: User | null,
  featureFlags?: FeatureFlags,
) {
  return visibleSettingsNavigation(SETTINGS_NAVIGATION, {
    isSuperuser: isSuperuser(user),
    canManageCharlie: canManageCharlie(user),
    extensionsEnabled: featureFlags?.["feature.extensions"] === true,
  });
}

export function commandPaletteClusterPages(groups: NavGroup[]): NavItem[] {
  return [
    ...new Map(
      groups.flatMap(navGroupItems).map((item) => [item.href, item]),
    ).values(),
  ];
}

// The command palette's "Pages" and "Cluster Pages" groups are derived from
// the same registry as the sidebar, filtered the same way, so a destination
// hidden from the sidebar (permission, feature flag, Charlie activation) is
// never surfaced in the palette either.
export function commandPalettePages(
  user: User | null,
  featureFlags: FeatureFlags | undefined,
  charlieActivated: boolean,
): NavItem[] {
  return filterNavGroups(
    globalNavGroups,
    user,
    featureFlags,
    charlieActivated,
  ).flatMap((group) => group.items);
}
