import { useId } from "react";
import { ChevronDown, ChevronUp, ExternalLink } from "lucide-react";

import { useClusterToolsStatus, useTools } from "@/lib/hooks/tools";
import {
  CollapsedNavItems,
  SidebarRailGroup,
} from "@/components/layout/sidebar-rail";
import type { NavGroup } from "@/components/layout/sidebar-navigation";
import { SidebarNavItems, type StarredNavControls } from "./sidebar-nav-items";

export function InstalledToolLinks({
  clusterId,
  collapsed,
  isOpen,
  onToggle,
}: {
  clusterId: string;
  collapsed: boolean;
  isOpen: boolean;
  onToggle: () => void;
}) {
  const { data: tools } = useTools();
  const { data: statuses } = useClusterToolsStatus(clusterId);

  if (!tools || !statuses) return null;

  const statusMap = new Map<string, (typeof statuses)[number]>();
  statuses.forEach((s) => statusMap.set(s.slug, s));

  // Build list of available UI links
  const uiLinks: Array<{ name: string; url: string }> = [];

  tools.forEach((tool) => {
    const status = statusMap.get(tool.slug);
    if (!status || status.status !== "installed") return;
    const ns = status.namespace || tool.defaultNamespace;
    if (!ns) return;

    // Main service UI
    if (tool.serviceName && tool.servicePort) {
      uiLinks.push({
        name: tool.name,
        url: `/api/v1/clusters/${clusterId}/proxy/service/${ns}/${tool.serviceName}:${tool.servicePort}${tool.servicePath || "/"}`,
      });
    }

    // Sub-services (Prometheus, Alertmanager, Kiali, etc.)
    tool.subServices?.forEach((sub) => {
      uiLinks.push({
        name: sub.name,
        url: `/api/v1/clusters/${clusterId}/proxy/service/${ns}/${sub.service}:${sub.port}/`,
      });
    });
  });

  if (uiLinks.length === 0) return null;

  if (collapsed) {
    return (
      <div className="space-y-0.5">
        {uiLinks.map((link) => (
          <a
            key={link.url}
            href={link.url}
            target="_blank"
            rel="noopener noreferrer"
            className="nav-item group justify-center px-0"
            title={`${link.name} (opens in new tab)`}
          >
            <ExternalLink className="h-4 w-4 text-muted-foreground group-hover:text-foreground" />
          </a>
        ))}
      </div>
    );
  }

  return (
    <div>
      <button
        onClick={onToggle}
        className="w-full flex items-center justify-between px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground transition-colors"
      >
        <span>Tool UIs</span>
        {isOpen ? (
          <ChevronUp className="h-3.5 w-3.5" />
        ) : (
          <ChevronDown className="h-3.5 w-3.5" />
        )}
      </button>
      {isOpen && (
        <div className="space-y-px">
          {uiLinks.map((link) => (
            <a
              key={link.url}
              href={link.url}
              target="_blank"
              rel="noopener noreferrer"
              className="flex items-center gap-2 px-3 py-1.5 mx-1 rounded-md text-sm text-muted-foreground hover:text-foreground hover:bg-accent/50 transition-colors"
            >
              <ExternalLink className="h-3.5 w-3.5 shrink-0" />
              <span className="truncate flex-1">{link.name}</span>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}

// Collapsible nav group - Rancher style
export function SidebarGroup({
  group,
  pathname,
  collapsed,
  counts,
  isOpen,
  onToggle,
  stars,
  onFlyoutChange,
}: {
  group: NavGroup;
  pathname: string;
  collapsed: boolean;
  counts?: Record<string, number>;
  isOpen: boolean;
  onToggle: () => void;
  stars?: StarredNavControls;
  onFlyoutChange?: (label: string, open: boolean) => void;
}) {
  const contentId = useId();
  if (collapsed) {
    return group.hideLabel ? (
      <CollapsedNavItems items={group.items} pathname={pathname} />
    ) : (
      <SidebarRailGroup
        group={group}
        pathname={pathname}
        counts={counts}
        stars={stars}
        onOpenChange={onFlyoutChange}
      />
    );
  }

  const expanded = group.hideLabel || isOpen;

  return (
    <div>
      {/* Group header with chevron on the right (Rancher style) */}
      {!group.hideLabel && (
        <button
          onClick={onToggle}
          aria-expanded={isOpen}
          aria-controls={contentId}
          className="w-full flex items-center justify-between px-3 py-2 text-sm font-semibold text-muted-foreground hover:text-foreground transition-colors"
        >
          <span>{group.label}</span>
          {isOpen ? (
            <ChevronUp className="h-3.5 w-3.5" />
          ) : (
            <ChevronDown className="h-3.5 w-3.5" />
          )}
        </button>
      )}
      {expanded && (
        <div id={contentId} className="space-y-px">
          <SidebarNavItems
            group={group}
            pathname={pathname}
            counts={counts}
            stars={stars}
          />
        </div>
      )}
    </div>
  );
}
