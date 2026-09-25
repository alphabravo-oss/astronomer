import { DELIVERY_DESTINATIONS } from "./delivery-navigation";
import {
  Activity,
  BarChart3,
  Bell,
  Box,
  FileText,
  FolderKanban,
  Layers,
  LayoutDashboard,
  Puzzle,
  Rocket,
  ScrollText,
  Search,
  Server,
  Settings,
  Shield,
  ShieldCheck,
  Sparkles,
  Star,
  Wrench,
} from "lucide-react";

import {
  can,
  isSuperuser,
  type PermissionVerb,
  type PermissionScope,
} from "@/lib/permissions";
import { navGroupItems, filterNavigationItems } from "./nav-group-items";
import type { FeatureFlags, FeatureFlagKey } from "@/lib/api/feature-flags";
import type { User } from "@/types";
import { SETTINGS_NAVIGATION } from "@/components/settings/settings-navigation";

export type NavItem = {
  label: string;
  href: string;
  icon: typeof Box;
  exact?: boolean;
  countKey?: string;
  resourceType?: string;
  ifHaveGroup?: string;
  ifHaveKind?: string;
  permission?: {
    resource: string;
    verb: PermissionVerb | "*";
  };
  projectPermission?: { resource: string; verb: PermissionVerb | "*" };
  description?: string;
  superuserOnly?: boolean;
  featureFlag?: FeatureFlagKey;
  // Opt-in flags stay hidden until the flags payload explicitly enables them
  // (missing/loading counts as off). Default-on flags still use === false so
  // they remain visible while the flags query hydrates.
  optIn?: boolean;
  requiresCharlieActivated?: boolean;
};

export type NavGroup = {
  label: string;
  items: NavItem[];
  subgroups?: Array<{ label: string; items: NavItem[] }>;
  defaultOpen?: boolean;
  // Rendered without a group header/toggle and always expanded — for the
  // small set of top-level destinations that don't belong under a labeled
  // section (e.g. the global "Home" group).
  hideLabel?: boolean;
  // Icon shown for the whole group in the collapsed sidebar rail
  // (sidebar-rail.tsx). Falls back to the first item's icon when omitted —
  // set it explicitly whenever that fallback would collide with another
  // group's icon in the same rail.
  icon?: typeof Box;
};

export const INSTALLED_TOOLS_NAV_GROUP = "Tool UIs";

export function activeNavGroupLabel(
  groups: readonly NavGroup[],
  pathname: string,
): string | null {
  return (
    groups.find((group) =>
      navGroupItems(group).some((item) =>
        item.exact ? pathname === item.href : pathname.startsWith(item.href),
      ),
    )?.label ?? null
  );
}

export function defaultOpenNavGroupLabel(
  groups: readonly NavGroup[],
  pathname: string,
): string | null {
  return (
    activeNavGroupLabel(groups, pathname) ??
    groups.find((group) => group.defaultOpen)?.label ??
    null
  );
}

// Default (global) navigation groups
export const globalNavGroups: NavGroup[] = [
  {
    label: "Home",
    hideLabel: true,
    items: [
      {
        label: "Overview",
        href: "/dashboard",
        icon: LayoutDashboard,
        exact: true,
      },
      {
        label: "Clusters",
        href: "/dashboard/clusters",
        icon: Server,
        permission: { resource: "clusters", verb: "list" },
      },
      {
        label: "Search",
        href: "/dashboard/search",
        icon: Search,
      },
      {
        label: "Charlie",
        href: "/dashboard/charlie",
        icon: Sparkles,
        permission: { resource: "charlie", verb: "read" },
        featureFlag: "feature.charlie",
        requiresCharlieActivated: true,
      },
    ],
  },
  {
    label: "Continuous Delivery",
    icon: Rocket,
    items: DELIVERY_DESTINATIONS,
  },
  {
    label: "Observability",
    icon: BarChart3,
    items: [
      {
        label: "Metrics",
        href: "/dashboard/monitoring",
        exact: true,
        icon: BarChart3,
        permission: { resource: "monitoring", verb: "read" },
        featureFlag: "feature.monitoring",
      },
      {
        label: "Shared stacks",
        href: "/dashboard/monitoring/stacks",
        icon: Layers,
        permission: { resource: "monitoring", verb: "read" },
        featureFlag: "feature.monitoring",
      },
      {
        label: "Alerting",
        href: "/dashboard/alerting",
        icon: Bell,
        permission: { resource: "alerts", verb: "read" },
      },
      {
        label: "Logging",
        href: "/dashboard/logging",
        icon: ScrollText,
        permission: { resource: "logging", verb: "read" },
      },
    ],
  },
  {
    label: "Security",
    icon: ShieldCheck,
    items: [
      {
        label: "Security",
        href: "/dashboard/security",
        icon: ShieldCheck,
        permission: { resource: "security", verb: "read" },
        featureFlag: "feature.security",
      },
      {
        label: "Cluster Agents",
        href: "/dashboard/agents",
        icon: Activity,
        permission: { resource: "cluster_agents", verb: "read" },
      },
    ],
  },
  {
    label: "Users & Access",
    icon: Shield,
    items: [
      {
        label: "RBAC",
        href: "/dashboard/rbac",
        icon: Shield,
        permission: { resource: "rbac", verb: "read" },
      },
      {
        label: "Projects",
        href: "/dashboard/projects",
        icon: FolderKanban,
        permission: { resource: "projects", verb: "list" },
        featureFlag: "feature.projects",
      },
      {
        label: "Audit Log",
        href: "/dashboard/audit",
        icon: FileText,
        permission: { resource: "audit_logs", verb: "read" },
      },
    ],
  },
  {
    label: "Configuration",
    icon: Layers,
    items: [
      {
        label: "Onboarding templates",
        href: "/dashboard/cluster-templates",
        icon: Layers,
        permission: { resource: "cluster_templates", verb: "list" },
      },
      {
        label: "Cluster Tools",
        href: "/dashboard/tools",
        icon: Wrench,
        permission: { resource: "catalog", verb: "read" },
        featureFlag: "feature.catalog",
      },
      // Helm marketplace lives on the cluster (Apps), matching Rancher Apps.
      // Estate-wide repos stay reachable from Apps → Repositories.
      {
        label: "Extensions",
        href: "/dashboard/extensions",
        icon: Puzzle,
        permission: { resource: "settings", verb: "read" },
        featureFlag: "feature.extensions",
        optIn: true,
      },
      // Superuser-only hub. Prefix-match so Dex/SSO under /settings/auth
      // highlights Settings instead of a sibling Auth row.
      {
        label: "Settings",
        href: "/dashboard/settings",
        icon: Settings,
        superuserOnly: true,
      },
    ],
  },
];

// The single label registry for breadcrumbs, the command palette, and the
// sidebar itself: given a route href, return the same label the nav (or the
// settings hub) shows for it, so a destination never carries a different
// name in different chrome.
export function navLabelForHref(href: string): string | undefined {
  for (const group of globalNavGroups) {
    const item = group.items.find((candidate) => candidate.href === href);
    if (item) return item.label;
  }
  for (const group of SETTINGS_NAVIGATION) {
    const item = group.items.find((candidate) => candidate.href === href);
    if (item) return item.title;
  }
  return undefined;
}

export function withFavoriteNavigation(
  groups: NavGroup[],
  favorites: readonly string[],
): NavGroup[] {
  if (favorites.length === 0) return groups;
  const byHref = new Map(
    groups.flatMap((group) => group.items).map((item) => [item.href, item]),
  );
  const items = favorites.flatMap((href) => {
    const item = byHref.get(href);
    return item ? [{ ...item, icon: Star }] : [];
  });
  return items.length > 0
    ? [{ label: "Favorites", items, defaultOpen: true, icon: Star }, ...groups]
    : groups;
}

export { getClusterNavGroups } from "@/components/layout/cluster-nav-groups";

export function filterNavGroups(
  groups: NavGroup[],
  user: User | null,
  featureFlags?: FeatureFlags,
  charlieActivated = false,
  scope: PermissionScope = { type: "global" },
): NavGroup[] {
  return filterNavigationItems(groups, (item) => {
    if (item.featureFlag) {
      if (item.optIn) {
        if (featureFlags?.[item.featureFlag] !== true) return false;
      } else if (featureFlags?.[item.featureFlag] === false) {
        return false;
      }
    }
    if (item.requiresCharlieActivated && !charlieActivated) return false;
    if (item.superuserOnly) return isSuperuser(user);
    if (
      item.projectPermission &&
      can(user, item.projectPermission.resource, item.projectPermission.verb, {
        type: "project",
      })
    )
      return true;
    if (!item.permission) return true;
    return can(user, item.permission.resource, item.permission.verb, scope);
  });
}
