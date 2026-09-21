import { useState } from "react";

import {
  defaultOpenNavGroupLabel,
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

// Sidebar groups stay open across navigation instead of collapsing back to a
// single accordion section. Open state is remembered per nav scope (global
// vs. cluster context) in localStorage, and the group containing the active
// route is always unioned into the open set — navigating never closes a
// group that was already open.
export function useOpenNavGroups(
  scope: SidebarNavScope,
  navGroups: readonly NavGroup[],
  pathname: string,
) {
  const [openGroups, setOpenGroups] = useState<Set<string>>(() => {
    const persisted = loadPersistedOpenGroups(scope);
    const active = defaultOpenNavGroupLabel(navGroups, pathname);
    return new Set(active ? [...persisted, active] : persisted);
  });
  const [tracked, setTracked] = useState({ scope, navGroups, pathname });

  if (
    tracked.scope !== scope ||
    tracked.navGroups !== navGroups ||
    tracked.pathname !== pathname
  ) {
    const scopeChanged = tracked.scope !== scope;
    setTracked({ scope, navGroups, pathname });
    setOpenGroups((current) => {
      const next = new Set(current);
      if (scopeChanged) {
        for (const group of loadPersistedOpenGroups(scope)) next.add(group);
      }
      const active = defaultOpenNavGroupLabel(navGroups, pathname);
      if (active) next.add(active);
      return next;
    });
  }

  const toggleGroup = (label: string) => {
    setOpenGroups((current) => {
      const next = new Set(current);
      if (next.has(label)) next.delete(label);
      else next.add(label);
      persistOpenGroups(scope, next);
      return next;
    });
  };

  return { openGroups, toggleGroup };
}
