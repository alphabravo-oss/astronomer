import {
  Activity,
  BarChart3,
  Bell,
  Box,
  Boxes,
  Crosshair,
  FileText,
  FolderKanban,
  GitBranch,
  Layers,
  LayoutDashboard,
  Puzzle,
  Rocket,
  Route,
  ScrollText,
  Search,
  Server,
  Settings,
  Shield,
  ShieldCheck,
  SlidersHorizontal,
  SlidersVertical,
  Sparkles,
  Star,
  Wrench,
} from "lucide-react";

import { can, isSuperuser, type PermissionVerb } from "@/lib/permissions";
import type { FeatureFlags, FeatureFlagKey } from "@/lib/api/feature-flags";
import type { User } from "@/types";
import { SETTINGS_NAVIGATION } from "@/components/settings/settings-navigation";

export type NavItem = {
  label: string;
  href: string;
  icon: typeof Box;
  exact?: boolean;
  countKey?: string;
  permission?: {
    resource: string;
    verb: PermissionVerb | "*";
  };
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
  defaultOpen?: boolean;
  // Rendered without a group header/toggle and always expanded — for the
  // small set of top-level destinations that don't belong under a labeled
  // section (e.g. the global "Home" group).
  hideLabel?: boolean;
};

export const INSTALLED_TOOLS_NAV_GROUP = "Tool UIs";

export function activeNavGroupLabel(
  groups: readonly NavGroup[],
  pathname: string,
): string | null {
  return (
    groups.find((group) =>
      group.items.some((item) =>
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
const deliveryListPermission = {
  resource: "delivery_targets",
  verb: "list",
} as const;

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
    items: [
      {
        label: "Estate",
        href: "/dashboard/delivery",
        icon: Rocket,
        permission: deliveryListPermission,
        exact: true,
      },
      {
        label: "Deployments",
        href: "/dashboard/delivery/deployments",
        icon: Layers,
        permission: deliveryListPermission,
      },
      {
        label: "Rollouts",
        href: "/dashboard/delivery/rollouts",
        icon: Route,
        permission: deliveryListPermission,
      },
      {
        label: "Sources",
        href: "/dashboard/delivery/sources",
        icon: GitBranch,
        permission: deliveryListPermission,
      },
      {
        label: "Bundles",
        href: "/dashboard/delivery/bundles",
        icon: Boxes,
        permission: deliveryListPermission,
      },
      {
        label: "Targets",
        href: "/dashboard/delivery/targets",
        icon: Crosshair,
        permission: deliveryListPermission,
      },
      {
        label: "Templates",
        href: "/dashboard/delivery/configuration-templates",
        icon: SlidersHorizontal,
        permission: deliveryListPermission,
      },
      {
        label: "Overrides",
        href: "/dashboard/delivery/override-sets",
        icon: SlidersVertical,
        permission: deliveryListPermission,
      },
    ],
  },
  {
    label: "Observability",
    items: [
      {
        label: "Metrics",
        href: "/dashboard/monitoring",
        icon: BarChart3,
        permission: { resource: "monitoring", verb: "read" },
        featureFlag: "feature.monitoring",
      },
      // Shared Thanos / Alertmanager lifecycle. It lives under the settings URL
      // because the API does (/settings/monitoring/...), but it is surfaced
      // here rather than only on the settings hub: the hub is superuser-only,
      // while these endpoints authorize on monitoring:read/update, so a
      // monitoring admin who is not a superuser would otherwise never find it.
      {
        label: "Shared stacks",
        href: "/dashboard/settings/monitoring",
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
    ? [{ label: "Favorites", items, defaultOpen: true }, ...groups]
    : groups;
}

export { getClusterNavGroups } from "@/components/layout/cluster-nav-groups";

export function filterNavGroups(
  groups: NavGroup[],
  user: User | null,
  featureFlags?: FeatureFlags,
  charlieActivated = false,
): NavGroup[] {
  return groups
    .map((group) => ({
      ...group,
      items: group.items.filter((item) => {
        if (item.featureFlag) {
          if (item.optIn) {
            if (featureFlags?.[item.featureFlag] !== true) return false;
          } else if (featureFlags?.[item.featureFlag] === false) {
            return false;
          }
        }
        if (item.requiresCharlieActivated && !charlieActivated) return false;
        if (item.superuserOnly) return isSuperuser(user);
        if (!item.permission) return true;
        return can(user, item.permission.resource, item.permission.verb);
      }),
    }))
    .filter((group) => group.items.length > 0);
}
