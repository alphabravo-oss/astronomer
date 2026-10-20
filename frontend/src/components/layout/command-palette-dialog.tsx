import { useCallback, useState } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import {
  Server,
  Folder,
  BookOpen,
  Search,
  Plus,
  Sparkles,
} from "lucide-react";
import { useUIStore } from "@/lib/store";
import { useAuthStore } from "@/lib/store";
import {
  useCharlieActivated,
  useClusters,
  useFeatureFlags,
} from "@/lib/hooks/clusters";
import { useProjects } from "@/lib/hooks/projects";
import type { SearchableResourceType } from "@/lib/api/resource-search";
import type { Cluster, Project } from "@/types";
import {
  commandPalettePages,
  commandPaletteClusterPages,
  commandPaletteSettings,
} from "@/components/layout/command-palette-pages";
import { useSidebarNavigation } from "./use-sidebar-navigation";
import { useCharlieOptional } from "@/components/charlie/charlie-context";
import {
  CommandGroup,
  CommandItem,
  CommandPalette,
  StatusDot,
  type StatusTone,
} from "@/ui";

// Extract the cluster id from a /dashboard/clusters/<id>/... path, skipping the
// static sub-routes that aren't real cluster ids (mirrors the sidebar logic).
function clusterIdFromPath(pathname: string): string | undefined {
  const match = pathname.match(/^\/dashboard\/clusters\/([^/]+)/);
  const seg = match?.[1];
  return seg && seg !== "new" && seg !== "register" ? seg : undefined;
}

const resourceSearches: Array<{
  name: string;
  type: SearchableResourceType;
  description: string;
}> = [
  { name: "Search pods", type: "pods", description: "Across connected clusters" },
  {
    name: "Search workloads",
    type: "deployments",
    description: "Deployments by name",
  },
  {
    name: "Search namespaces",
    type: "namespaces",
    description: "Namespace inventory",
  },
  { name: "Search services", type: "services", description: "Service endpoints" },
  { name: "Search ingresses", type: "ingresses", description: "Ingress routing" },
  { name: "Search nodes", type: "nodes", description: "Node inventory" },
];

const runbookLinks = [
  {
    name: "Delivery recovery",
    to: "/dashboard/delivery/deployments",
    search: { phase: "failed" },
    description: "Reconciliation failures, drift and rollback status",
  },
  {
    name: "Backup recovery",
    to: "/dashboard/settings/backup",
    description: "Management-plane dump, restore drill, key wrapping",
  },
  {
    name: "Operations queues",
    to: "/dashboard/settings/operations",
    description: "Task outbox, queues, dead letters",
  },
  {
    name: "Audit investigation",
    to: "/dashboard/audit",
    description: "Who, what, where, and request ids",
  },
] as const;

const clusterTone: Record<string, StatusTone> = {
  active: "success",
  error: "error",
  disconnected: "neutral",
  pending: "info",
};

export function CommandPaletteDialog() {
  const routerNavigate = useNavigate();
  const pathname = useLocation({ select: (location) => location.pathname });
  const pageHref = useProjectPageHref();
  const currentClusterId = clusterIdFromPath(pathname);
  const { navGroups } = useSidebarNavigation(currentClusterId, true);
  const { commandPaletteOpen, setCommandPaletteOpen } = useUIStore();
  const { data: clustersData } = useClusters({ pageSize: 50 });
  const { data: projectsData } = useProjects({ pageSize: 25 });
  const { data: featureFlags } = useFeatureFlags();
  const { activated: charlieActivated } = useCharlieActivated();
  const charlie = useCharlieOptional();
  const user = useAuthStore((state) => state.user);
  const [search, setSearch] = useState("");
  const settingsGroups = commandPaletteSettings(user, featureFlags);
  const globalPages = commandPalettePages(user, featureFlags, charlieActivated);
  const clusterContextPages = currentClusterId
    ? commandPaletteClusterPages(navGroups)
    : [];

  const close = useCallback(() => {
    setCommandPaletteOpen(false);
    setSearch("");
  }, [setCommandPaletteOpen]);

  const go = useCallback(
    (to: string, searchParams?: Record<string, string>) => {
      void routerNavigate({ to, ...(searchParams ? { search: searchParams } : {}) });
      close();
    },
    [close, routerNavigate],
  );

  const selectCluster = useCallback(
    (cluster: Cluster) => {
      void routerNavigate({
        to: "/dashboard/clusters/$id",
        params: { id: cluster.id },
      });
      close();
    },
    [close, routerNavigate],
  );

  const selectProject = useCallback(
    (project: Project) => {
      void routerNavigate({
        to: "/dashboard/projects/$id",
        params: { id: project.id },
      });
      close();
    },
    [close, routerNavigate],
  );

  return (
    <CommandPalette
      open={commandPaletteOpen}
      onOpenChange={(open) => {
        if (!open) close();
        else setCommandPaletteOpen(true);
      }}
      value={search}
      onValueChange={setSearch}
      placeholder="Search pages, clusters, resources and actions"
    >
      <CommandGroup heading="Actions">
        <CommandItem
          value="register cluster action"
          icon={Plus}
          title="Register cluster"
          description="Adopt a cluster you already run"
          onSelect={() => go("/dashboard/clusters/register")}
        />
        {charlie ? (
          <CommandItem
            value="ask charlie assistant action"
            icon={Sparkles}
            title="Ask Charlie"
            description="Open the assistant drawer"
            hotkey="⌘ J"
            onSelect={() => {
              charlie.setOpen(true);
              close();
            }}
          />
        ) : null}
      </CommandGroup>

      <CommandGroup heading="Pages">
        {globalPages.map((page) => (
          <CommandItem
            key={page.href}
            value={`${page.href} ${page.label} page`}
            icon={page.icon}
            title={page.label}
            onSelect={() => go(pageHref(page.href))}
          />
        ))}
      </CommandGroup>

      {currentClusterId && clusterContextPages.length > 0 ? (
        <CommandGroup heading="This cluster">
          {clusterContextPages.map((page) => (
            <CommandItem
              key={page.href}
              value={`${page.href} ${page.label} ${page.description ?? ""} cluster`}
              description={page.description}
              icon={page.icon}
              title={page.label}
              onSelect={() => go(pageHref(page.href))}
            />
          ))}
        </CommandGroup>
      ) : null}

      {settingsGroups.map((group) => (
        <CommandGroup key={group.label} heading={`Settings · ${group.label}`}>
          {group.items.map((item) => (
            <CommandItem
              key={item.href}
              value={`${item.title} ${item.description} settings ${group.label}`}
              icon={item.icon}
              title={item.title}
              description={item.description}
              onSelect={() => go(item.href)}
            />
          ))}
        </CommandGroup>
      ))}

      <CommandGroup heading="Search resources">
        {resourceSearches.map((item) => (
          <CommandItem
            key={item.type}
            value={`${item.name} ${item.type} kubernetes resources ${search}`}
            icon={Search}
            title={item.name}
            description={item.description}
            onSelect={() =>
              go("/dashboard/search", {
                type: item.type,
                ...(search.trim() ? { name: search.trim() } : {}),
              })
            }
          />
        ))}
      </CommandGroup>

      {clustersData?.data && clustersData.data.length > 0 ? (
        <CommandGroup heading="Clusters">
          {clustersData.data.map((cluster) => (
            <CommandItem
              key={cluster.id}
              value={`${cluster.name} ${cluster.displayName} ${cluster.provider} ${cluster.region} cluster`}
              icon={Server}
              title={cluster.displayName || cluster.name}
              description={[cluster.environment, cluster.region]
                .filter(Boolean)
                .join(" · ")}
              trailing={
                <StatusDot
                  tone={clusterTone[cluster.status] ?? "neutral"}
                  label={cluster.status}
                  className="text-meta text-muted-foreground"
                />
              }
              onSelect={() => selectCluster(cluster)}
            />
          ))}
        </CommandGroup>
      ) : null}

      {projectsData?.data && projectsData.data.length > 0 ? (
        <CommandGroup heading="Projects">
          {projectsData.data.map((project) => (
            <CommandItem
              key={project.id}
              value={`${project.name} ${project.displayName} project namespaces ${project.namespaces?.join(" ") ?? ""}`}
              icon={Folder}
              title={project.displayName || project.name}
              description={`${project.namespaces?.length ?? 0} namespaces`}
              onSelect={() => selectProject(project)}
            />
          ))}
        </CommandGroup>
      ) : null}

      <CommandGroup heading="Runbooks">
        {runbookLinks.map((item) => (
          <CommandItem
            key={item.to}
            value={`${item.name} ${item.description} runbook docs operations recovery`}
            icon={BookOpen}
            title={item.name}
            description={item.description}
            onSelect={() =>
              go(item.to, "search" in item ? { ...item.search } : undefined)
            }
          />
        ))}
      </CommandGroup>
    </CommandPalette>
  );
}

function useProjectPageHref() {
  const project = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  ).get("project");
  const pageHref = (href: string) =>
    project && href.includes("/delivery")
      ? `${href}?project=${encodeURIComponent(project)}`
      : href;
  return pageHref;
}
