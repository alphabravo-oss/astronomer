import { useCallback, useState } from "react";
import type { ReactNode } from "react";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { Command } from "cmdk";
import { Server, ArrowRight, Folder, BookOpen, Search } from "lucide-react";
import type { LucideIcon } from "lucide-react";
import { useUIStore } from "@/lib/store";
import { useAuthStore } from "@/lib/store";
import {
  useCharlieActivated,
  useClusters,
  useFeatureFlags,
} from "@/lib/hooks/clusters";
import { useProjects } from "@/lib/hooks/projects";
import type { SearchableResourceType } from "@/lib/api/resource-search";
import { OverlayShell } from "@/components/ui/overlay-shell";
import type { Cluster, Project } from "@/types";
import {
  commandPalettePages,
  commandPaletteClusterPages,
  commandPaletteSettings,
} from "@/components/layout/command-palette-pages";
import { useSidebarNavigation } from "./use-sidebar-navigation";

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
  {
    name: "Search pods",
    type: "pods",
    description: "Across connected clusters",
  },
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
  {
    name: "Search services",
    type: "services",
    description: "Service endpoints",
  },
  {
    name: "Search ingresses",
    type: "ingresses",
    description: "Ingress routing",
  },
  { name: "Search nodes", type: "nodes", description: "Node inventory" },
];

const runbookLinks = [
  {
    name: "Delivery recovery",
    to: "/dashboard/delivery/deployments",
    search: { phase: "failed" },
    description: "Reconciliation failures, drift, and rollback status",
  },
  {
    name: "Backup recovery",
    to: "/dashboard/settings/backup",
    description: "Astronomer dump, restore drill, encryption-key wrapping",
  },
  {
    name: "Operations queues",
    to: "/dashboard/settings/operations",
    description: "Task outbox, queues, dead letters",
  },
  {
    name: "Audit investigation",
    to: "/dashboard/audit",
    description: "Who, what, where, and request IDs",
  },
] as const;

function paletteItemClassName() {
  return "flex items-center gap-3 px-3 py-2.5 rounded-lg text-sm text-muted-foreground cursor-pointer data-[selected=true]:bg-accent data-[selected=true]:text-foreground";
}

function CommandRow({
  value,
  icon: Icon,
  title,
  description,
  right,
  onSelect,
}: {
  value: string;
  icon: LucideIcon;
  title: string;
  description?: string;
  right?: ReactNode;
  onSelect: () => void;
}) {
  return (
    <Command.Item
      value={value}
      onSelect={onSelect}
      className={paletteItemClassName()}
    >
      <Icon className="h-4 w-4 shrink-0" />
      <div className="min-w-0 flex-1">
        <p className="truncate">{title}</p>
        {description ? (
          <p className="truncate text-xs text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {right ?? (
        <ArrowRight className="h-3.5 w-3.5 opacity-0 data-[selected=true]:opacity-100" />
      )}
    </Command.Item>
  );
}

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

  const selectCluster = useCallback(
    (cluster: Cluster) => {
      // The cluster context is encoded in the URL slug, not in any global
      // store — navigating is sufficient.
      void routerNavigate({
        to: "/dashboard/clusters/$id",
        params: { id: cluster.id },
      });
      close();
    },
    [close, routerNavigate],
  );

  const selectProject = useProjectNavigation(routerNavigate, close);

  if (!commandPaletteOpen) return null;

  return (
    <OverlayShell onClose={() => setCommandPaletteOpen(false)}>
      <div className="fixed top-[20%] left-1/2 -translate-x-1/2 w-full max-w-lg">
        <Command
          className="rounded-xl border border-border bg-popover shadow-2xl overflow-hidden"
          shouldFilter={true}
        >
          <div className="flex items-center border-b border-border px-4">
            <Search className="h-4 w-4 text-muted-foreground shrink-0" />
            <Command.Input
              value={search}
              onValueChange={setSearch}
              placeholder="Search clusters, pages, actions..."
              className="flex-1 h-12 px-3 bg-transparent text-sm text-foreground placeholder:text-muted-foreground
                focus:outline-hidden"
            />
            <kbd
              className="hidden sm:inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded-sm border
              border-border bg-muted text-[10px] font-mono text-muted-foreground"
            >
              ESC
            </kbd>
          </div>

          <Command.List className="max-h-80 overflow-y-auto p-2">
            <Command.Empty className="py-8 text-center text-sm text-muted-foreground">
              No results found.
            </Command.Empty>

            {/* Navigation */}
            <Command.Group
              heading="Pages"
              className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5"
            >
              {globalPages.map((page) => (
                <CommandRow
                  key={page.href}
                  value={`${page.href} ${page.label}`}
                  icon={page.icon}
                  title={page.label}
                  onSelect={() => {
                    void routerNavigate({ to: pageHref(page.href) });
                    close();
                  }}
                />
              ))}
            </Command.Group>

            {settingsGroups.map((group) => (
              <Command.Group
                key={group.label}
                heading={`Settings · ${group.label}`}
                className="mt-2 px-2 py-1.5 text-xs font-semibold uppercase tracking-wider text-muted-foreground/60"
              >
                {group.items.map((item) => (
                  <CommandRow
                    key={item.href}
                    value={`${item.title} ${item.description} settings ${group.label}`}
                    icon={item.icon}
                    title={item.title}
                    description={item.description}
                    onSelect={() => {
                      void routerNavigate({ to: item.href });
                      close();
                    }}
                  />
                ))}
              </Command.Group>
            ))}

            {currentClusterId && (
              <Command.Group
                heading="Cluster Pages"
                className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2"
              >
                {clusterContextPages.map((page) => (
                  <CommandRow
                    key={page.href}
                    value={`${page.href} ${page.label} ${page.description} cluster`}
                    description={page.description}
                    icon={page.icon}
                    title={page.label}
                    onSelect={() => {
                      void routerNavigate({ to: pageHref(page.href) });
                      close();
                    }}
                  />
                ))}
              </Command.Group>
            )}

            <Command.Group
              heading="Resource Search"
              className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2"
            >
              {resourceSearches.map((item) => (
                <CommandRow
                  key={item.type}
                  value={`${item.name} ${item.type} kubernetes resources ${search}`}
                  icon={Search}
                  title={item.name}
                  description={item.description}
                  onSelect={() => {
                    void routerNavigate({
                      to: "/dashboard/search",
                      search: {
                        type: item.type,
                        ...(search.trim() ? { name: search.trim() } : {}),
                      },
                    });
                    close();
                  }}
                />
              ))}
            </Command.Group>

            {/* Clusters */}
            {clustersData?.data && clustersData.data.length > 0 && (
              <Command.Group
                heading="Clusters"
                className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2"
              >
                {clustersData.data.map((cluster) => (
                  <CommandRow
                    key={cluster.id}
                    value={`${cluster.name} ${cluster.displayName} ${cluster.provider} ${cluster.region}`}
                    icon={Server}
                    title={cluster.displayName}
                    description={`${cluster.provider} / ${cluster.region}`}
                    right={
                      <span
                        className={`inline-flex h-2 w-2 rounded-full ${
                          cluster.status === "active"
                            ? "bg-status-success"
                            : cluster.status === "error"
                              ? "bg-status-error"
                              : "bg-status-neutral"
                        }`}
                      />
                    }
                    onSelect={() => selectCluster(cluster)}
                  />
                ))}
              </Command.Group>
            )}

            {projectsData?.data && projectsData.data.length > 0 && (
              <Command.Group
                heading="Projects"
                className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2"
              >
                {projectsData.data.map((project) => (
                  <CommandRow
                    key={project.id}
                    value={`${project.name} ${project.displayName} project namespaces ${project.namespaces?.join(" ") ?? ""}`}
                    icon={Folder}
                    title={project.displayName || project.name}
                    description={`${project.namespaces?.length ?? 0} namespaces`}
                    onSelect={() => selectProject(project)}
                  />
                ))}
              </Command.Group>
            )}

            <Command.Group
              heading="Runbooks"
              className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2"
            >
              {runbookLinks.map((item) => (
                <CommandRow
                  key={item.to}
                  value={`${item.name} ${item.description} runbook docs operations recovery`}
                  icon={BookOpen}
                  title={item.name}
                  description={item.description}
                  onSelect={() => {
                    void routerNavigate({
                      to: item.to,
                      ...("search" in item ? { search: item.search } : {}),
                    });
                    close();
                  }}
                />
              ))}
            </Command.Group>

            {/* Quick Actions */}
            <Command.Group
              heading="Actions"
              className="text-xs text-muted-foreground/60 font-semibold uppercase tracking-wider px-2 py-1.5 mt-2"
            >
              <CommandRow
                value="Register new cluster"
                icon={Server}
                title="Register New Cluster"
                onSelect={() => {
                  void routerNavigate({ to: "/dashboard/clusters/register" });
                  close();
                }}
              />
            </Command.Group>
          </Command.List>
        </Command>
      </div>
    </OverlayShell>
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

function useProjectNavigation(
  routerNavigate: ReturnType<typeof useNavigate>,
  close: () => void,
) {
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
  return selectProject;
}
