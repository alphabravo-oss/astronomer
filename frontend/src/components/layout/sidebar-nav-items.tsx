import { Link, useLocation } from "@tanstack/react-router";
import { Star } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { cn } from "@/lib/utils";
import type { NavGroup, NavItem } from "./sidebar-navigation";

export interface StarredNavControls {
  types: readonly string[];
  disabled: boolean;
  toggle: (resourceType: string) => void;
}

export function SidebarNavItems({
  group,
  pathname,
  counts,
  stars,
  onNavigate,
}: {
  group: NavGroup;
  pathname: string;
  counts?: Record<string, number>;
  stars?: StarredNavControls;
  onNavigate?: () => void;
}) {
  const searchStr = useLocation({ select: (location) => location.searchStr });
  const project = new URLSearchParams(searchStr).get("project");
  const candidates = [
    ...group.items,
    ...(group.subgroups?.flatMap((item) => item.items) ?? []),
  ];
  const activeHref = candidates
    .filter((item) =>
      item.exact
        ? pathname === item.href
        : pathname === item.href || pathname.startsWith(`${item.href}/`),
    )
    .sort((a, b) => b.href.length - a.href.length)[0]?.href;
  const renderItem = (item: NavItem) => {
    const Icon = item.icon;
    const active = item.href === activeHref;
    const count = item.countKey ? counts?.[item.countKey] : undefined;
    const starred =
      !!item.resourceType && !!stars?.types.includes(item.resourceType);
    return (
      <div key={item.href} className="group flex items-center gap-1 mx-1">
        <Link
          to={
            project && item.href.includes("/delivery")
              ? `${item.href}?project=${encodeURIComponent(project)}`
              : item.href
          }
          activeOptions={{ exact: true }}
          activeProps={{ "aria-current": active ? "page" : undefined }}
          onClick={onNavigate}
          aria-current={active ? "page" : undefined}
          className={cn(
            "flex min-w-0 flex-1 items-center gap-2 rounded-md px-3 py-1.5 text-sm transition-colors",
            active
              ? "bg-accent text-foreground font-medium"
              : "text-muted-foreground hover:text-foreground hover:bg-accent/50",
          )}
        >
          <Icon className="h-4 w-4 shrink-0" />
          <span className="truncate flex-1">{item.label}</span>
          {count !== undefined && (
            <span className="text-xs tabular-nums">{count}</span>
          )}
        </Link>
        {item.resourceType && stars && (
          <ActionButton
            intent="ghost"
            size="icon"
            aria-label={`${starred ? "Unstar" : "Star"} ${item.label}`}
            aria-pressed={starred}
            disabled={stars.disabled || (!starred && stars.types.length >= 20)}
            disabledReason={
              !starred && stars.types.length >= 20
                ? "You can star up to 20 resource types"
                : undefined
            }
            onClick={() => stars.toggle(item.resourceType!)}
            icon={
              <Star className={cn("h-3.5 w-3.5", starred && "fill-current")} />
            }
            className={cn(
              !starred &&
                "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus:opacity-100 [@media(hover:none)]:opacity-100",
            )}
          />
        )}
      </div>
    );
  };
  return (
    <>
      {group.items.map(renderItem)}
      {group.subgroups?.map((subgroup) => (
        <div key={subgroup.label}>
          <p className="px-3 pt-3 pb-1 text-2xs font-semibold uppercase text-muted-foreground">
            {subgroup.label}
          </p>
          {subgroup.items.map(renderItem)}
        </div>
      ))}
    </>
  );
}
