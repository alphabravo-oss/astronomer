import { useEffect } from "react";
import { useLocation } from "@tanstack/react-router";
import { ExternalLink, Star } from "lucide-react";

import { ExtensionNavItems } from "@/components/extensions/ExtensionNavItems";
import {
  INSTALLED_TOOLS_NAV_GROUP,
  type NavGroup,
  type NavItem,
} from "@/components/layout/sidebar-navigation";
import { useOpenNavGroups } from "@/components/layout/nav-open-groups";
import { OverlayBackdrop } from "@/components/ui/overlay-shell";
import { useUIStore } from "@/lib/store";
import { cn, formatK8sVersion } from "@/lib/utils";
import { useSidebarNavigation } from "./use-sidebar-navigation";
import { useStarredNavControls } from "./use-starred-nav-controls";
import { useMobileNavigation } from "./use-mobile-navigation";
import { useInstalledToolLinks } from "./use-installed-tool-links";
import { navGroupItems } from "./nav-group-items";
import {
  Rail,
  RailGroup,
  RailItem,
  SidebarBrand,
  SidebarFrame,
  SidebarGroup,
  SidebarItem,
  StatusDot,
  Tooltip,
} from "@/ui";
import { ActionButton } from "@/components/ui/action-button";

export function clusterIdFromPathname(pathname: string): string | undefined {
  const segment = pathname.match(/^\/dashboard\/clusters\/([^/]+)/)?.[1];
  return segment && segment !== "new" && segment !== "register"
    ? segment
    : undefined;
}

function isItemActive(item: NavItem, pathname: string): boolean {
  return item.exact
    ? pathname === item.href
    : pathname === item.href || pathname.startsWith(`${item.href}/`);
}

/** The longest matching href wins so nested routes highlight one row. */
function activeHref(items: NavItem[], pathname: string): string | undefined {
  return items
    .filter((item) => isItemActive(item, pathname))
    .sort((a, b) => b.href.length - a.href.length)[0]?.href;
}

function groupIsActive(group: NavGroup, pathname: string): boolean {
  return navGroupItems(group).some((item) => isItemActive(item, pathname));
}

/**
 * Two-level navigation (A1): a global rail of pillars that is always
 * visible, and either the pillar panel (global context) or the cluster panel
 * (six groups) beside it. Counts never appear here.
 */
export function Sidebar() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const searchStr = useLocation({ select: (location) => location.searchStr });
  const {
    sidebarCollapsed,
    mobileSidebarOpen,
    toggleSidebarCollapsed,
    setMobileSidebarOpen,
  } = useUIStore();

  const clusterId = clusterIdFromPathname(pathname);
  const isClusterContext = !!clusterId;
  const { cluster, navGroups } = useSidebarNavigation(clusterId);
  const { navGroups: globalGroups } = useSidebarNavigation(undefined);
  const stars = useStarredNavControls();

  useEffect(() => {
    setMobileSidebarOpen(false);
  }, [pathname, setMobileSidebarOpen]);

  const { ref: mobileNavigationRef, closed: mobileNavigationClosed } =
    useMobileNavigation(mobileSidebarOpen, () => setMobileSidebarOpen(false));

  const collapsed = sidebarCollapsed && !mobileSidebarOpen;
  const { openGroups, toggleGroup } = useOpenNavGroups(
    isClusterContext ? "cluster" : "global",
    navGroups,
    pathname,
  );
  const project = new URLSearchParams(searchStr).get("project");
  const withProject = (href: string) =>
    project && href.includes("/delivery")
      ? `${href}?project=${encodeURIComponent(project)}`
      : href;

  const renderItems = (group: NavGroup, starrable: boolean) => {
    const items = navGroupItems(group);
    const active = activeHref(items, pathname);
    const row = (item: NavItem) => {
      const starred =
        !!item.resourceType && !!stars.types.includes(item.resourceType);
      return (
        <SidebarItem
          key={item.href}
          to={withProject(item.href)}
          icon={item.icon}
          label={item.label}
          active={item.href === active}
          trailing={
            starrable && item.resourceType ? (
              <ActionButton
                intent="ghost"
                size="icon-xs"
                aria-label={`${starred ? "Unstar" : "Star"} ${item.label}`}
                aria-pressed={starred}
                disabled={
                  stars.disabled || (!starred && stars.types.length >= 20)
                }
                disabledReason={
                  !starred && stars.types.length >= 20
                    ? "You can star up to 20 resource types"
                    : undefined
                }
                onClick={() => stars.toggle(item.resourceType!)}
                icon={
                  <Star
                    className={cn("h-3.5 w-3.5", starred && "fill-current")}
                  />
                }
                className={cn(
                  !starred &&
                    "opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 focus:opacity-100 [@media(hover:none)]:opacity-100",
                )}
              />
            ) : undefined
          }
        />
      );
    };
    return (
      <>
        {group.items.map(row)}
        {group.subgroups?.map((subgroup) => (
          <div key={subgroup.label} className="pt-1">
            <p className="px-3 pb-1 pt-2 text-micro font-semibold text-muted-foreground">
              {subgroup.label}
            </p>
            {subgroup.items.map(row)}
          </div>
        ))}
      </>
    );
  };

  // ---- Global rail (always visible on desktop) ----
  const rail = (
    <Rail className="w-(--rail-w) shrink-0 border-r border-sidebar-border">
      {globalGroups.map((group) => {
        const active = isClusterContext
          ? group.label === "Clusters"
          : groupIsActive(group, pathname);
        const Icon = group.icon ?? group.items[0]?.icon;
        if (!Icon) return null;
        const items = navGroupItems(group);
        if (items.length <= 1 || group.hideLabel) {
          const target = items[0];
          if (!target) return null;
          return (
            <RailItem
              key={group.label}
              to={withProject(target.href)}
              icon={Icon}
              label={group.label}
              active={active}
            />
          );
        }
        return (
          <RailGroup
            key={group.label}
            icon={Icon}
            label={group.label}
            active={active}
          >
            {renderItems(group, false)}
          </RailGroup>
        );
      })}
    </Rail>
  );

  // ---- Panel content ----
  const panel = isClusterContext ? (
    <>
      {navGroups.map((group) => (
        <SidebarGroup
          key={group.label}
          label={group.label}
          open={openGroups.has(group.label)}
          onToggle={() => toggleGroup(group.label)}
        >
          {renderItems(group, true)}
        </SidebarGroup>
      ))}
      <InstalledToolLinks
        clusterId={clusterId!}
        open={openGroups.has(INSTALLED_TOOLS_NAV_GROUP)}
        onToggle={() => toggleGroup(INSTALLED_TOOLS_NAV_GROUP)}
      />
    </>
  ) : (
    <>
      {navGroups.map((group) => {
        // A pillar with one destination renders as a flat row: a header that
        // repeats the row's label would be noise.
        const flat =
          group.hideLabel ||
          (navGroupItems(group).length === 1 && !group.subgroups?.length);
        return (
          <SidebarGroup
            key={group.label}
            label={flat ? undefined : group.label}
            open={flat || openGroups.has(group.label)}
            onToggle={flat ? undefined : () => toggleGroup(group.label)}
          >
            {renderItems(group, false)}
          </SidebarGroup>
        );
      })}
      <ExtensionNavItems pathname={pathname} collapsed={false} />
    </>
  );

  const clusterStatusTone =
    cluster?.status === "active"
      ? "success"
      : cluster?.status === "error"
        ? "error"
        : "neutral";

  return (
    <>
      {mobileSidebarOpen && (
        <OverlayBackdrop
          ariaLabel="Close navigation"
          className="backdrop-blur-[1px] lg:hidden"
          onClose={() => setMobileSidebarOpen(false)}
        />
      )}
      <SidebarFrame
        ref={mobileNavigationRef}
        inert={mobileNavigationClosed}
        mobileOpen={mobileSidebarOpen}
        collapsed={collapsed}
        onToggleCollapsed={toggleSidebarCollapsed}
        navLabel={isClusterContext ? "Cluster" : "Primary"}
        brand={<SidebarBrand to="/dashboard" subtitle="by AlphaBravo" />}
        className={cn(
          !collapsed &&
            "w-[calc(var(--rail-w)+var(--sidebar-w))] lg:w-[calc(var(--rail-w)+var(--sidebar-w))]",
        )}
        context={
          isClusterContext && !collapsed ? (
            <div className="flex items-start gap-0">
              <div className="w-(--rail-w) shrink-0" />
              <div className="min-w-0 flex-1 border-b border-sidebar-border px-4 py-2.5">
                <p className="truncate text-sm font-semibold text-foreground">
                  {cluster?.displayName || cluster?.name || "Cluster"}
                </p>
                <div className="mt-0.5 flex items-center gap-2 text-micro text-muted-foreground">
                  <StatusDot
                    tone={clusterStatusTone}
                    label={cluster?.status ? capitalize(cluster.status) : "Unknown"}
                    className="text-micro"
                  />
                  {cluster?.kubernetesVersion ? (
                    <span className="font-mono">
                      {formatK8sVersion(cluster.kubernetesVersion)}
                    </span>
                  ) : null}
                </div>
              </div>
            </div>
          ) : null
        }
      >
        {collapsed ? (
          rail
        ) : (
          <div className="flex h-full items-stretch">
            <div className="-ml-2 -mt-2 -mb-2 flex shrink-0 flex-col pt-2">
              {rail}
            </div>
            <div className="min-w-0 flex-1 pl-2">{panel}</div>
          </div>
        )}
      </SidebarFrame>
    </>
  );
}

function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

/** Installed tool UIs (proxied links) at the bottom of the cluster panel. */
function InstalledToolLinks({
  clusterId,
  open,
  onToggle,
}: {
  clusterId: string;
  open: boolean;
  onToggle: () => void;
}) {
  const links = useInstalledToolLinks(clusterId);
  if (links.length === 0) return null;
  return (
    <SidebarGroup label={INSTALLED_TOOLS_NAV_GROUP} open={open} onToggle={onToggle}>
      {links.map((link) => (
        <Tooltip key={link.url} content={`${link.name} (opens in a new tab)`}>
          <a
            href={link.url}
            target="_blank"
            rel="noopener noreferrer"
            aria-label={`${link.name} (opens in a new tab)`}
            className="flex h-8 items-center gap-2.5 rounded-md px-3 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent/60 hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
          >
            <ExternalLink className="h-4 w-4 shrink-0" aria-hidden />
            <span className="min-w-0 flex-1 truncate">{link.name}</span>
          </a>
        </Tooltip>
      ))}
    </SidebarGroup>
  );
}
