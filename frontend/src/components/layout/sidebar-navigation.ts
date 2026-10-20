import { DELIVERY_DESTINATIONS } from "./delivery-navigation";
import {
  Activity,
  BarChart3,
  Bell,
  Box,
  FileText,
  FolderKanban,
  Gauge,
  Layers,
  LayoutDashboard,
  LayoutGrid,
  Package,
  Puzzle,
  Rocket,
  ScrollText,
  Server,
  Settings,
  Shield,
  ShieldCheck,
  Star,
  Users,
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
  // section (e.g. the global "Overview" group).
  hideLabel?: boolean;
  // Icon shown for the whole group in the rail (one icon per pillar).
  icon?: typeof Box;
  /** The pillar's landing page, used by the global rail. */
  href?: string;
};

export const INSTALLED_TOOLS_NAV_GROUP = "Tool links";

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

/**
 * Global navigation: seven pillars (A1 "Operator Console"). The rail shows one
 * icon per pillar; the expanded sidebar shows the pillar's items. Labels are
 * sentence case; one icon per item within a group.
 */
export const globalNavGroups: NavGroup[] = [
  {
    label: "Overview",
    hideLabel: true,
    icon: LayoutDashboard,
    href: "/dashboard",
    items: [
      {
        label: "Overview",
        href: "/dashboard",
        icon: LayoutDashboard,
        exact: true,
      },
    ],
  },
  {
    label: "Clusters",
    icon: Server,
    href: "/dashboard/clusters",
    defaultOpen: true,
    items: [
      {
        label: "Clusters",
        href: "/dashboard/clusters",
        icon: Server,
        permission: { resource: "clusters", verb: "list" },
      },
      {
        label: "Onboarding templates",
        href: "/dashboard/cluster-templates",
        icon: Layers,
        permission: { resource: "cluster_templates", verb: "list" },
      },
      {
        label: "Tools",
        href: "/dashboard/tools",
        icon: Wrench,
        permission: { resource: "catalog", verb: "read" },
        featureFlag: "feature.catalog",
      },
      {
        label: "Catalog",
        href: "/dashboard/catalog",
        icon: Package,
        permission: { resource: "catalog", verb: "read" },
        featureFlag: "feature.catalog",
      },
    ],
  },
  {
    label: "Delivery",
    icon: Rocket,
    href: "/dashboard/delivery",
    items: DELIVERY_DESTINATIONS,
  },
  {
    label: "Observability",
    icon: Gauge,
    href: "/dashboard/monitoring",
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
        icon: LayoutGrid,
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
    href: "/dashboard/security",
    items: [
      {
        label: "Security",
        href: "/dashboard/security",
        icon: ShieldCheck,
        permission: { resource: "security", verb: "read" },
        featureFlag: "feature.security",
      },
      {
        label: "Agents",
        href: "/dashboard/agents",
        icon: Activity,
        permission: { resource: "cluster_agents", verb: "read" },
      },
    ],
  },
  {
    label: "Access",
    icon: Users,
    href: "/dashboard/rbac",
    items: [
      {
        label: "Access control",
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
        label: "Audit log",
        href: "/dashboard/audit",
        icon: FileText,
        permission: { resource: "audit_logs", verb: "read" },
      },
    ],
  },
  {
    label: "Settings",
    icon: Settings,
    href: "/dashboard/settings",
    items: [
      // Superuser-only. Prefix-match so Dex/SSO under /settings/auth
      // highlights Settings instead of a sibling Auth row.
      {
        label: "Settings",
        href: "/dashboard/settings",
        icon: Settings,
        superuserOnly: true,
      },
      {
        label: "Extensions",
        href: "/dashboard/extensions",
        icon: Puzzle,
        permission: { resource: "settings", verb: "read" },
        featureFlag: "feature.extensions",
        optIn: true,
      },
    ],
  },
];

// The single label registry for breadcrumbs, the command palette, and the
// sidebar itself: given a route href, return the same label the nav (or the
// settings registry) shows for it, so a destination never carries a different
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
    ? [
        {
          label: "Favorites",
          items,
          defaultOpen: true,
          icon: Star,
          href: items[0].href,
        },
        ...groups,
      ]
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
