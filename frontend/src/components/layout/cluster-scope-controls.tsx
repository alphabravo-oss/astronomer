import { Command } from "cmdk";
import { useDebouncedValue } from "@tanstack/react-pacer";
import {
  Check,
  ChevronsUpDown,
  FolderKanban,
  Layers3,
  Search,
} from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";

import {
  type ClusterNamespaceScope,
  useClusterNamespaceScope,
} from "@/lib/cluster-scope";
import { useProject, useProjectSearch } from "@/lib/hooks/projects";
import { cn } from "@/lib/utils";
import type { Cluster, ClusterStatus } from "@/types";

export function clusterIdFromPath(pathname: string): string | undefined {
  const segment = pathname.match(/^\/dashboard\/clusters\/([^/]+)/)?.[1];
  return segment && segment !== "new" && segment !== "register"
    ? segment
    : undefined;
}

export function useDismissable(
  open: boolean,
  close: () => void,
  restoreFocus?: () => void,
) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: MouseEvent) => {
      if (!ref.current?.contains(event.target as Node)) close();
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        close();
        requestAnimationFrame(() => restoreFocus?.());
      }
    };
    document.addEventListener("mousedown", dismiss);
    document.addEventListener("keydown", escape);
    return () => {
      document.removeEventListener("mousedown", dismiss);
      document.removeEventListener("keydown", escape);
    };
  }, [close, open, restoreFocus]);
  return ref;
}

const statusClass: Record<ClusterStatus, string> = {
  active: "bg-status-success",
  error: "bg-status-error",
  disconnected: "bg-status-neutral",
  pending: "bg-status-info",
};

export function ClusterOption({ cluster }: { cluster: Cluster }) {
  return (
    <>
      <span
        aria-hidden="true"
        className={cn(
          "h-2 w-2 shrink-0 rounded-full",
          statusClass[cluster.status] ?? "bg-status-neutral",
        )}
      />
      <span className="min-w-0 flex-1">
        <span className="sr-only">{cluster.status} cluster. </span>
        <span className="block truncate text-sm text-foreground">
          {cluster.displayName || cluster.name}
        </span>
        <span className="block truncate text-xs text-muted-foreground">
          {[cluster.environment, cluster.region].filter(Boolean).join(" · ")}
        </span>
      </span>
    </>
  );
}

/** Multi-namespace cluster scope. Restricted callers cannot choose an unsafe all scope. */
export function ClusterScopeControls({ clusterId }: { clusterId: string }) {
  const scope = useClusterNamespaceScope(clusterId);
  return (
    <>
      <ProjectScopePicker clusterId={clusterId} scope={scope} />
      <NamespaceScopePicker scope={scope} />
    </>
  );
}

function ProjectScopePicker({
  clusterId,
  scope,
}: {
  clusterId: string;
  scope: ClusterNamespaceScope;
}) {
  const [open, setOpen] = useState(false);
  const [term, setTerm] = useState("");
  const [debouncedTerm] = useDebouncedValue(term, { wait: 250 });
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const projectsQuery = useProjectSearch(clusterId, debouncedTerm, open);
  const selectedProjectQuery = useProject(scope.selectedProjectId ?? "");
  const projects = useMemo(
    () => projectsQuery.data?.pages.flatMap((page) => page.data) ?? [],
    [projectsQuery.data?.pages],
  );
  const selectedProject =
    projects.find((project) => project.id === scope.selectedProjectId) ??
    (selectedProjectQuery.data?.clusterId === clusterId
      ? selectedProjectQuery.data
      : undefined);
  useEffect(() => {
    if (open) searchRef.current?.focus();
  }, [open]);
  const close = () => setOpen(false);
  const restoreFocus = () => triggerRef.current?.focus();
  const ref = useDismissable(open, close, restoreFocus);
  const selectProject = (projectId: string | null) => {
    const project = projects.find((candidate) => candidate.id === projectId);
    scope.setProjectScope(project?.id ?? null, project?.namespaces ?? null);
    close();
    requestAnimationFrame(restoreFocus);
  };
  return (
    <div ref={ref} className="relative shrink-0">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((value) => !value)}
        disabled={projectsQuery.isLoading || !scope.ready}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`Project scope: ${selectedProject?.displayName || selectedProject?.name || "All projects"}`}
        className="inline-flex h-8 max-w-44 items-center gap-1.5 rounded-md border border-border px-2 text-xs text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50 sm:px-2.5"
      >
        <FolderKanban className="h-3.5 w-3.5 shrink-0" />
        <span className="hidden truncate lg:inline">
          {selectedProject?.displayName ||
            selectedProject?.name ||
            "All projects"}
        </span>
        <ChevronsUpDown className="h-3 w-3 shrink-0" />
      </button>
      {open ? (
        <Command
          shouldFilter={false}
          className="fixed left-3 right-3 top-14 z-50 overflow-hidden rounded-lg border border-border bg-popover shadow-xl sm:absolute sm:left-auto sm:right-0 sm:top-full sm:mt-1 sm:w-72"
        >
          <div className="flex items-center border-b border-border px-3">
            <Search className="h-4 w-4 text-muted-foreground" />
            <Command.Input
              ref={searchRef}
              value={term}
              onValueChange={setTerm}
              placeholder="Find a project..."
              className="h-10 min-w-0 flex-1 bg-transparent px-2 text-sm outline-hidden placeholder:text-muted-foreground"
            />
          </div>
          <Command.List className="max-h-72 overflow-y-auto p-1" role="listbox">
            <Command.Item
              value="all projects"
              onSelect={() => selectProject(null)}
              className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm data-[selected=true]:bg-accent"
            >
              <Check
                className={cn(
                  "h-4 w-4",
                  scope.selectedProjectId === null
                    ? "opacity-100"
                    : "opacity-0",
                )}
              />
              All projects
            </Command.Item>
            {projects.map((project) => (
              <Command.Item
                key={project.id}
                value={`${project.displayName} ${project.name}`}
                onSelect={() => selectProject(project.id)}
                className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm data-[selected=true]:bg-accent"
              >
                <Check
                  className={cn(
                    "h-4 w-4",
                    project.id === scope.selectedProjectId
                      ? "opacity-100"
                      : "opacity-0",
                  )}
                />
                <span className="min-w-0">
                  <span className="block truncate">
                    {project.displayName || project.name}
                  </span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {project.namespaces.length} namespace
                    {project.namespaces.length === 1 ? "" : "s"}
                  </span>
                </span>
              </Command.Item>
            ))}
            {projectsQuery.isLoading || term.trim() !== debouncedTerm.trim() ? (
              <Command.Loading className="px-3 py-4 text-sm text-muted-foreground">
                Searching projects...
              </Command.Loading>
            ) : !projectsQuery.isError && projects.length === 0 ? (
              <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">
                No projects in this cluster.
              </Command.Empty>
            ) : null}
            {projectsQuery.isError ? (
              <div role="alert" className="px-3 py-4 text-sm">
                Could not load projects.
                <button
                  type="button"
                  onClick={() => void projectsQuery.refetch()}
                  className="ml-2 underline"
                >
                  Retry
                </button>
              </div>
            ) : null}
            {projectsQuery.hasNextPage ? (
              <Command.Item
                value="load-more-projects"
                disabled={projectsQuery.isFetchingNextPage}
                onSelect={() => void projectsQuery.fetchNextPage()}
                className="cursor-pointer rounded-md px-3 py-2 text-center text-sm text-primary data-[selected=true]:bg-accent"
              >
                {projectsQuery.isFetchingNextPage
                  ? "Loading..."
                  : "Load more projects"}
              </Command.Item>
            ) : null}
          </Command.List>
        </Command>
      ) : null}
    </div>
  );
}

function NamespaceScopePicker({ scope }: { scope: ClusterNamespaceScope }) {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const close = () => setOpen(false);
  const restoreFocus = () => triggerRef.current?.focus();
  const ref = useDismissable(open, close, restoreFocus);
  const selected = scope.selectedNamespaces;
  useEffect(() => {
    if (open) searchRef.current?.focus();
  }, [open]);
  const label = useMemo(() => {
    if (!scope.ready) return "Namespaces";
    if (selected === null) return "All namespaces";
    if (selected?.length === 0) return "No namespaces";
    if (selected?.length === 1) return selected[0];
    return `${selected?.length ?? 0} namespaces`;
  }, [scope.ready, selected]);

  const toggle = (namespace: string) => {
    const current = selected ?? scope.availableNamespaces;
    scope.setSelectedNamespaces(
      current.includes(namespace)
        ? current.filter((value) => value !== namespace)
        : [...current, namespace],
    );
  };

  return (
    <div ref={ref} className="relative shrink-0">
      <button
        ref={triggerRef}
        type="button"
        onClick={() => setOpen((value) => !value)}
        disabled={!scope.ready}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={`Namespace scope: ${label}`}
        className="inline-flex h-8 max-w-28 items-center gap-1.5 rounded-md border border-border px-2 text-xs text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50 sm:max-w-48 sm:px-2.5"
      >
        <Layers3 className="h-3.5 w-3.5 shrink-0" />
        <span className="truncate">{label}</span>
        <ChevronsUpDown className="h-3 w-3 shrink-0" />
      </button>
      {open ? (
        <Command className="fixed left-3 right-3 top-14 z-50 overflow-hidden rounded-lg border border-border bg-popover shadow-xl sm:absolute sm:left-auto sm:right-0 sm:top-full sm:mt-1 sm:w-72">
          <div className="flex items-center border-b border-border px-3">
            <Search className="h-4 w-4 text-muted-foreground" />
            <Command.Input
              ref={searchRef}
              placeholder="Filter namespaces..."
              className="h-10 min-w-0 flex-1 bg-transparent px-2 text-sm outline-hidden placeholder:text-muted-foreground"
            />
          </div>
          <Command.List className="max-h-72 overflow-y-auto p-1" role="listbox">
            {!scope.restricted ? (
              <Command.Item
                value="all namespaces"
                onSelect={() => scope.setSelectedNamespaces(null)}
                className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm data-[selected=true]:bg-accent"
              >
                <Check
                  className={cn(
                    "h-4 w-4",
                    selected === null ? "opacity-100" : "opacity-0",
                  )}
                />
                All authorized namespaces
              </Command.Item>
            ) : null}
            {scope.availableNamespaces.map((namespace) => (
              <Command.Item
                key={namespace}
                value={namespace}
                onSelect={() => toggle(namespace)}
                className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-2 text-sm data-[selected=true]:bg-accent"
              >
                <span className="flex h-4 w-4 items-center justify-center rounded-sm border border-border">
                  {selected === null || selected?.includes(namespace) ? (
                    <Check className="h-3 w-3 text-primary" />
                  ) : null}
                </span>
                <span className="font-mono text-xs">{namespace}</span>
              </Command.Item>
            ))}
          </Command.List>
          {scope.restricted ? (
            <p className="border-t border-border px-3 py-2 text-xs text-muted-foreground">
              Scope is limited to namespaces granted by your roles.
            </p>
          ) : null}
        </Command>
      ) : null}
    </div>
  );
}
