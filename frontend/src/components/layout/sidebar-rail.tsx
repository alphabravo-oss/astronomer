import { useEffect, useId, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Link as RouterLink } from "@tanstack/react-router";

import type { NavGroup, NavItem } from "@/components/layout/sidebar-navigation";
import { cn } from "@/lib/utils";
import { navGroupItems } from "./nav-group-items";
import { SidebarNavItems, type StarredNavControls } from "./sidebar-nav-items";

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
  stars,
}: {
  group: NavGroup;
  pathname: string;
  counts?: Record<string, number>;
  stars?: StarredNavControls;
}) {
  const [open, setOpen] = useState(false);
  const contentId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const flyoutRef = useRef<HTMLElement>(null);
  const [position, setPosition] = useState({ top: 8, left: 64 });
  const close = () => setOpen(false);
  useEffect(() => {
    if (!open) return;
    flyoutRef.current?.querySelector<HTMLAnchorElement>("a")?.focus();
    const dismiss = (event: MouseEvent) => {
      const target = event.target as Node;
      if (
        !triggerRef.current?.contains(target) &&
        !flyoutRef.current?.contains(target)
      )
        setOpen(false);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setOpen(false);
        triggerRef.current?.focus();
      }
    };
    const reposition = () => {
      const rect = triggerRef.current?.getBoundingClientRect();
      if (rect)
        setPosition({
          left: rect.right + 4,
          top: Math.max(8, Math.min(rect.top, window.innerHeight * 0.3 - 8)),
        });
    };
    reposition();
    document.addEventListener("mousedown", dismiss);
    document.addEventListener("keydown", escape);
    window.addEventListener("resize", reposition);
    window.addEventListener("scroll", reposition, true);
    return () => {
      document.removeEventListener("mousedown", dismiss);
      document.removeEventListener("keydown", escape);
      window.removeEventListener("resize", reposition);
      window.removeEventListener("scroll", reposition, true);
    };
  }, [open]);
  const items = navGroupItems(group);
  const GroupIcon = group.icon ?? items[0]?.icon;
  const isActiveGroup = items.some((item) => isItemActive(item, pathname));

  if (!GroupIcon) return null;

  return (
    <div className="relative">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((value) => !value)}
        title={group.label}
        aria-label={group.label}
        aria-controls={open ? contentId : undefined}
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
      {open &&
        createPortal(
          <nav
            ref={flyoutRef}
            id={contentId}
            aria-label={group.label}
            style={position}
            className="fixed z-50 max-h-[70vh] w-64 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-xl"
          >
            <p className="px-2 py-1.5 text-2xs font-semibold uppercase text-muted-foreground">
              {group.label}
            </p>
            <SidebarNavItems
              group={group}
              pathname={pathname}
              counts={counts}
              stars={stars}
              onNavigate={close}
            />
          </nav>,
          document.body,
        )}
    </div>
  );
}
