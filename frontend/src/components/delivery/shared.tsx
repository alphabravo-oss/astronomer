import { useProjectSelection } from "@/lib/cluster-scope-project";
import { useEffect, useMemo, type ReactNode } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate, useLocation } from "@tanstack/react-router";
import { useProject } from "@/lib/hooks/projects";
import { useQuery } from "@tanstack/react-query";
import { getProjects, getClusterProjects } from "@/lib/api/projects";
import { queryKeys } from "@/lib/query-keys";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PermissionState,
} from "@/components/ui/empty-state";
import { ArrowLeft, FolderKanban, PackageOpen } from "lucide-react";
import { cn } from "@/lib/utils";
import { useClusterScopeStore } from "@/lib/cluster-scope";

export type DeliveryListTab =
  "sources" | "bundles" | "targets" | "rollouts" | "deployments";

export function clusterDeliveryPath(
  clusterId: string,
  tab: string = "",
): string {
  const suffix = tab ? `/${tab}` : "";
  return `/dashboard/clusters/${clusterId}/delivery${suffix}`;
}

export function withProjectQuery(path: string, projectId?: string): string {
  if (!projectId) return path;
  const join = path.includes("?") ? "&" : "?";
  return `${path}${join}project=${encodeURIComponent(projectId)}`;
}

export function deliveryEntityPath(
  tab: DeliveryListTab,
  entityId: string,
  opts: { clusterId?: string; projectId?: string } = {},
): string {
  const encoded = encodeURIComponent(entityId);
  const path = opts.clusterId
    ? `${clusterDeliveryPath(opts.clusterId, tab)}/${encoded}`
    : `/dashboard/delivery/${tab}/${encoded}`;
  return withProjectQuery(path, opts.projectId);
}

export function projectClusterId(project: {
  clusterId?: string;
  clusterIds?: string[];
}): string | undefined {
  return project.clusterId || project.clusterIds?.[0];
}

export function projectBoundToCluster(
  project: { clusterId?: string; clusterIds?: string[] },
  clusterId: string,
): boolean {
  return (
    project.clusterId === clusterId ||
    Boolean(project.clusterIds?.includes(clusterId))
  );
}

export function useDeliveryProjectScope(opts?: { clusterId?: string }) {
  const projects = useQuery({
    queryKey: queryKeys.projects.deliveryScope(opts?.clusterId),
    queryFn: ({ signal }) =>
      opts?.clusterId
        ? getClusterProjects(opts.clusterId, { pageSize: 25 }, { signal })
        : getProjects({ pageSize: 25 }, { signal }),
    throwOnError: false,
  });
  const searchStr = useLocation({ select: (location) => location.searchStr });
  const search = useMemo(() => new URLSearchParams(searchStr), [searchStr]);
  const clusterId = opts?.clusterId;
  const rememberedProjectId = useClusterScopeStore((state) =>
    clusterId ? state.projectByCluster[clusterId] : null,
  );
  const requested =
    search.get("project") ?? (clusterId ? (rememberedProjectId ?? "") : "");
  const selected = useProject(requested);
  const rows = useMemo(() => {
    const all = projects.isError ? [] : [...(projects.data?.data ?? [])];
    if (
      selected.data &&
      !selected.isError &&
      !all.some((item) => item.id === selected.data.id)
    )
      all.push(selected.data);
    if (!clusterId) return all;
    return all.filter((project) => projectBoundToCluster(project, clusterId));
  }, [
    clusterId,
    projects.data?.data,
    projects.isError,
    selected.data,
    selected.isError,
  ]);
  const onlyProject = rows.length === 1 && !projects.data?.pagination.has_more;
  const projectId =
    requested &&
    !selected.isError &&
    rows.some((project) => project.id === requested)
      ? requested
      : !requested && onlyProject
        ? rows[0].id
        : "";

  const projectSelection = useProjectSelection(clusterId);
  const setProjectId = projectSelection.select;
  // Unique bounded project defaults use the same atomic transaction as all pickers.
  useEffect(() => {
    if (!requested && onlyProject) void setProjectId(rows[0].id);
  }, [requested, onlyProject, rows, setProjectId]);

  const projectQuery = requested ? selected : projects;
  return { projectId, projects: rows, projectQuery, setProjectId };
}

export function useDeliveryWorkspace() {
  const pathname = useLocation({ select: (location) => location.pathname });
  const clusterMatch = pathname.match(
    /^\/dashboard\/clusters\/([^/]+)\/delivery/,
  );
  const clusterId = clusterMatch?.[1];
  const scope = useDeliveryProjectScope({ clusterId });
  const listHref = (tab: DeliveryListTab | "") =>
    clusterId
      ? clusterDeliveryPath(clusterId, tab)
      : tab
        ? `/dashboard/delivery/${tab}`
        : "/dashboard/delivery";
  const entityHref = (tab: DeliveryListTab, entityId: string) =>
    deliveryEntityPath(tab, entityId, {
      clusterId,
      projectId: scope.projectId,
    });
  return { clusterId, listHref, entityHref, ...scope };
}

export function useDeliveryPageIndex(parameter = "page") {
  const pathname = useLocation({ select: (location) => location.pathname });
  const search = new URLSearchParams(
    useLocation({ select: (location) => location.searchStr }),
  );
  const navigate = useNavigate();
  const parsed = Number(search.get(parameter) ?? "0");
  const pageIndex = Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
  const setPageIndex = (nextPage: number) => {
    const next = new URLSearchParams(search);
    if (nextPage > 0) next.set(parameter, String(nextPage));
    else next.delete(parameter);
    void navigate({
      to: `${pathname}${next.size ? `?${next.toString()}` : ""}`,
      replace: true,
    });
  };
  return [pageIndex, setPageIndex] as const;
}

export function deliveryProjectLabel(project: {
  displayName: string;
  name: string;
}) {
  return project.displayName || project.name;
}

export function DeliveryShell({
  projectId,
  children,
}: {
  projectId: string;
  projects: Array<{
    id: string;
    displayName: string;
    name: string;
    clusterId?: string;
    clusterIds?: string[];
  }>;
  setProjectId: (id: string) => void;
  showProjectSelect?: boolean;
  children: ReactNode;
}) {
  const { clusterId } = useDeliveryWorkspace();
  // Workspace layouts own project selection; detail pages retain a fleet link.
  if (clusterId) return children;
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-center sm:justify-between">
        <RouterLink
          to="/dashboard/delivery"
          search={{ project: projectId }}
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to delivery fleet
        </RouterLink>
      </div>
      {children}
    </div>
  );
}

export function DeliveryProjectGate({
  projectId,
  loading,
  error,
  projectsCount,
  permission,
  allowed = true,
  onRetry,
  children,
}: {
  projectId: string;
  loading: boolean;
  error: boolean;
  projectsCount: number;
  permission: string;
  allowed?: boolean;
  onRetry: () => void;
  children: ReactNode;
}) {
  if (loading) return <LoadingState title="Loading project access" />;
  if (error)
    return (
      <ErrorState
        description="Project access could not be loaded."
        onRetry={onRetry}
      />
    );
  if (projectsCount === 0) {
    return (
      <EmptyState
        icon={PackageOpen}
        title="No projects available"
        description="Create or request access to a project before configuring delivery."
        actionLabel="Go to projects"
        actionHref="/dashboard/projects"
      />
    );
  }
  if (!projectId) {
    return (
      <EmptyState
        icon={FolderKanban}
        title="Choose a project"
        description="Delivery resources are isolated by project. Select one above to continue."
        // terminal: the action is the project selector rendered above this panel.
        terminal
      />
    );
  }
  if (!allowed) return <PermissionState permission={permission} />;
  return children;
}

export function DeliveryPhaseBadge({ value }: { value: string }) {
  const normalized = value.toLowerCase();
  const status = [
    "ready",
    "succeeded",
    "verified",
    "compatible",
    "connected",
    "released",
  ].includes(normalized)
    ? "healthy"
    : [
          "failed",
          "rollback_failed",
          "incompatible",
          "rejected",
          "revoked",
          "disconnected",
        ].includes(normalized)
      ? "failed"
      : [
            "degraded",
            "blocked",
            "paused",
            "timed_out",
            "upgrade_required",
            "stale",
            "inventory_missing",
          ].includes(normalized)
        ? "warning"
        : [
              "progressing",
              "reconciling",
              "resolving",
              "applying",
              "running",
              "queued",
            ].includes(normalized)
          ? "running"
          : "pending";
  return <StatusBadge status={status} label={value.replaceAll("_", " ")} />;
}

export function DetailGrid({ children }: { children: ReactNode }) {
  return (
    <dl className="grid gap-3 rounded-lg border border-border bg-card p-4 sm:grid-cols-2 xl:grid-cols-4">
      {children}
    </dl>
  );
}

export function Detail({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <dt className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
        {label}
      </dt>
      <dd
        className={cn(
          "mt-1 break-words text-sm text-foreground",
          mono && "font-mono text-xs",
        )}
      >
        {value || "—"}
      </dd>
    </div>
  );
}

export function ErrorMessage({ error }: { error: unknown }) {
  const message =
    error instanceof Error ? error.message : "The operation failed.";
  return (
    <p
      role="alert"
      className="rounded-md border border-status-error/30 bg-status-error/10 px-3 py-2 text-sm text-status-error"
    >
      {message}
    </p>
  );
}

export const primaryButton =
  "inline-flex h-9 items-center justify-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground transition-opacity hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50";
export const secondaryButton =
  "inline-flex h-9 items-center justify-center gap-2 rounded-md border border-border bg-background px-4 text-sm font-medium text-foreground hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50";
export const dangerButton =
  "inline-flex h-9 items-center justify-center gap-2 rounded-md bg-status-error px-4 text-sm font-medium text-white hover:opacity-90 disabled:cursor-not-allowed disabled:opacity-50";
export const inputClass =
  "h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-hidden focus:ring-2 focus:ring-ring";
export const textareaClass =
  "min-h-24 w-full rounded-md border border-border bg-background px-3 py-2 text-sm focus:outline-hidden focus:ring-2 focus:ring-ring";
