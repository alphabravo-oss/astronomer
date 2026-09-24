import { useState } from "react";

import {
  defaultOpenNavGroupLabel,
  INSTALLED_TOOLS_NAV_GROUP,
  type NavGroup,
} from "@/components/layout/sidebar-navigation";

export type SidebarNavScope = "global" | "cluster";

const STORAGE_PREFIX = "astronomer.sidebar.openGroups.";

function storageKey(scope: SidebarNavScope): string {
  return `${STORAGE_PREFIX}${scope}`;
}

function loadPersistedOpenGroups(scope: SidebarNavScope): string[] {
  try {
    const raw = window.localStorage.getItem(storageKey(scope));
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed)
      ? parsed.filter((value): value is string => typeof value === "string")
      : [];
  } catch {
    // Private browsing, disabled storage, or a corrupt value — fall back to
    // no persisted groups rather than breaking the sidebar.
    return [];
  }
}

function persistOpenGroups(scope: SidebarNavScope, groups: Set<string>): void {
  try {
    window.localStorage.setItem(storageKey(scope), JSON.stringify([...groups]));
  } catch {
    // Ignore — open state just won't survive a reload.
  }
}

function availableGroup(
  label: string,
  scope: SidebarNavScope,
  groups: readonly NavGroup[],
): boolean {
  return (
    groups.some((group) => group.label === label) ||
    (scope === "cluster" && label === INSTALLED_TOOLS_NAV_GROUP)
  );
}

function initialOpenGroups(
  scope: SidebarNavScope,
  groups: readonly NavGroup[],
  pathname: string,
): Set<string> {
  const label =
    defaultOpenNavGroupLabel(groups, pathname) ??
    loadPersistedOpenGroups(scope)
      .reverse()
      .find((value) => availableGroup(value, scope, groups));
  return new Set(label ? [label] : []);
}

// One section at a time, with independent global/cluster preferences.
// Navigation reveals the active section; manual toggles may close every section.
export function useOpenNavGroups(
  scope: SidebarNavScope,
  navGroups: readonly NavGroup[],
  pathname: string,
) {
  const [openGroups, setOpenGroups] = useState<Set<string>>(() =>
    initialOpenGroups(scope, navGroups, pathname),
  );
  const [tracked, setTracked] = useState({ scope, navGroups, pathname });

  if (
    tracked.scope !== scope ||
    tracked.navGroups !== navGroups ||
    tracked.pathname !== pathname
  ) {
    const scopeChanged = tracked.scope !== scope;
    const routeChanged = tracked.pathname !== pathname;
    const activeChanged =
      defaultOpenNavGroupLabel(tracked.navGroups, pathname) !==
      defaultOpenNavGroupLabel(navGroups, pathname);
    setTracked({ scope, navGroups, pathname });
    setOpenGroups((current) => {
      if (scopeChanged || routeChanged || activeChanged)
        return initialOpenGroups(scope, navGroups, pathname);
      return new Set(
        [...current].filter((label) => availableGroup(label, scope, navGroups)),
      );
    });
  }

  const toggleGroup = (label: string) => {
    setOpenGroups((current) => {
      if (!availableGroup(label, scope, navGroups)) return current;
      const next = new Set(current.has(label) ? [] : [label]);
      persistOpenGroups(scope, next);
      return next;
    });
  };

  return { openGroups, toggleGroup };
}
