import { useState } from "react";
import { Link as RouterLink } from "@tanstack/react-router";

import { useDismissable } from "@/components/layout/cluster-scope-controls";
import type { NavGroup, NavItem } from "@/components/layout/sidebar-navigation";
import { cn } from "@/lib/utils";

function isItemActive(item: NavItem, pathname: string): boolean {
  return item.exact ? pathname === item.href : pathname.startsWith(item.href);
}

/**
 * Header-less groups (e.g. the global "Home" group) render their items
 * directly as icons in the collapsed rail — there is no group label to
 * collapse behind a flyout.
 */
export function CollapsedNavItems({
  items,
  pathname,
}: {
  items: NavItem[];
  pathname: string;
}) {
  return (
    <div className="space-y-0.5">
      {items.map((item) => {
        const Icon = item.icon;
        const active = isItemActive(item, pathname);
        return (
          <RouterLink
            key={item.href}
            to={item.href}
            className={cn(
              "nav-item group justify-center px-0",
              active && "active",
            )}
            title={item.label}
          >
            <Icon
              className={cn(
                "h-4 w-4 shrink-0",
                active
                  ? "text-foreground"
                  : "text-muted-foreground group-hover:text-foreground",
              )}
            />
          </RouterLink>
        );
      })}
    </div>
  );
}

/**
 * One control per labeled group in the collapsed sidebar rail. Rancher's
 * collapsed nav shows every item as its own icon (dozens of near-identical
 * icons in cluster context); this instead shows one icon per group and
 * opens a flyout listing that group's items on click/Enter.
 */
export function SidebarRailGroup({
  group,
  pathname,
  counts,
}: {
  group: NavGroup;
  pathname: string;
  counts?: Record<string, number>;
}) {
  const [open, setOpen] = useState(false);
  const close = () => setOpen(false);
  const ref = useDismissable(open, close);
  const GroupIcon = group.icon ?? group.items[0]?.icon;
  const isActiveGroup = group.items.some((item) =>
    isItemActive(item, pathname),
  );

  if (!GroupIcon) return null;

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        title={group.label}
        aria-label={group.label}
        aria-haspopup="menu"
        aria-expanded={open}
        className={cn(
          "nav-item group w-full justify-center px-0",
          isActiveGroup && "active",
        )}
      >
        <GroupIcon
          className={cn(
            "h-4 w-4 shrink-0",
            isActiveGroup
              ? "text-foreground"
              : "text-muted-foreground group-hover:text-foreground",
          )}
        />
      </button>
      {open && (
        <div
          role="menu"
          aria-label={group.label}
          className="absolute left-full top-0 z-50 ml-1 w-56 rounded-lg border border-border bg-popover p-1 shadow-xl"
        >
          <p className="px-2 py-1.5 text-2xs font-semibold uppercase text-muted-foreground">
            {group.label}
          </p>
          {group.items.map((item) => {
            const Icon = item.icon;
            const active = isItemActive(item, pathname);
            const count =
              item.countKey && counts ? counts[item.countKey] : undefined;
            return (
              <RouterLink
                key={item.href}
                to={item.href}
                role="menuitem"
                onClick={close}
                className={cn(
                  "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors",
                  active
                    ? "bg-accent font-medium text-foreground"
                    : "text-muted-foreground hover:bg-accent/50 hover:text-foreground",
                )}
              >
                <Icon className="h-4 w-4 shrink-0" />
                <span className="min-w-0 flex-1 truncate">{item.label}</span>
                {count !== undefined && (
                  <span className="text-xs tabular-nums text-muted-foreground/60">
                    {count}
                  </span>
                )}
              </RouterLink>
            );
          })}
        </div>
      )}
    </div>
  );
}
