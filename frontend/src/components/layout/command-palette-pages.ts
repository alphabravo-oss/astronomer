import {
  filterNavGroups,
  getClusterNavGroups,
  globalNavGroups,
  type NavItem,
} from "@/components/layout/sidebar-navigation";
import type { FeatureFlags } from "@/lib/api/feature-flags";
import type { User } from "@/types";

// The command palette's "Pages" and "Cluster Pages" groups are derived from
// the same registry as the sidebar, filtered the same way, so a destination
// hidden from the sidebar (permission, feature flag, Charlie activation) is
// never surfaced in the palette either.
export function commandPalettePages(
  user: User | null,
  featureFlags: FeatureFlags | undefined,
  charlieActivated: boolean,
): NavItem[] {
  return filterNavGroups(globalNavGroups, user, featureFlags, charlieActivated).flatMap(
    (group) => group.items,
  );
}

export function commandPaletteClusterPages(
  clusterId: string,
  user: User | null,
  featureFlags: FeatureFlags | undefined,
  charlieActivated: boolean,
): NavItem[] {
  return filterNavGroups(
    getClusterNavGroups(clusterId, {}),
    user,
    featureFlags,
    charlieActivated,
  ).flatMap((group) => group.items);
}
