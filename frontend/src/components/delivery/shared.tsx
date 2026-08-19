import { useEffect, useMemo, type ReactNode } from "react";
import { Link } from "@/lib/link";
import { usePathname, useRouter, useSearchParams } from "@/lib/navigation";
import { useClusters, useProjects } from "@/lib/hooks";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PermissionState,
} from "@/components/ui/empty-state";
import { ArrowLeft, FolderKanban, PackageOpen } from "lucide-react";
import { cn } from "@/lib/utils";

export type DeliveryListTab =
  | "sources"
  | "bundles"
  | "targets"
  | "rollouts"
  | "deployments";

export function clusterDeliveryPath(clusterId: string, tab: string = ""): string {
  const suffix = tab ? `/${tab}` : "";
  return `/dashboard/clusters/${clusterId}/delivery${suffix}`;
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
  const projects = useProjects({ pageSize: 200 });
  const pathname = usePathname();
  const search = useSearchParams();
  const router = useRouter();
  const clusterId = opts?.clusterId;
  const rows = useMemo(() => {
    const all = projects.data?.data ?? [];
    if (!clusterId) return all;
    return all.filter((project) => projectBoundToCluster(project, clusterId));
  }, [clusterId, projects.data?.data]);
  const requested = search.get("project") ?? "";
  const projectId = rows.some((project) => project.id === requested)
    ? requested
    : rows.length === 1
      ? rows[0].id
      : "";

  // A one-project user should land on working data immediately. The URL is
  // still authoritative and is updated so deep links remain shareable.
  useEffect(() => {
    if (requested || rows.length !== 1) return;
    const next = new URLSearchParams(search);
    next.set("project", rows[0].id);
    router.replace(`${pathname}?${next.toString()}`);
  }, [pathname, requested, router, rows, search]);

  const setProjectId = (id: string) => {
    const next = new URLSearchParams(search);
    if (id) next.set("project", id);
    else next.delete("project");
    next.delete("page");
    next.delete("version_page");
    next.delete("cluster_page");
    router.replace(`${pathname}${next.size ? `?${next.toString()}` : ""}`);
  };

  return { projectId, projects: rows, projectQuery: projects, setProjectId };
}

export function useDeliveryWorkspace() {
  const pathname = usePathname();
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
  return { clusterId, listHref, ...scope };
}

export function RedirectDeliveryList({ tab }: { tab: DeliveryListTab }) {
  const { projectId, projects, projectQuery } = useDeliveryProjectScope();
  const search = useSearchParams();
  const searchKey = search.toString();
  const router = useRouter();
  useEffect(() => {
    if (projectQuery.isLoading) return;
    const project =
      projects.find((item) => item.id === projectId) ??
      (projects.length === 1 ? projects[0] : undefined);
    const clusterId = project ? projectClusterId(project) : undefined;
    const next = new URLSearchParams(searchKey);
    if (project?.id) next.set("project", project.id);
    if (clusterId) {
      router.replace(
        `${clusterDeliveryPath(clusterId, tab)}${next.size ? `?${next.toString()}` : ""}`,
      );
      return;
    }
    router.replace("/dashboard/delivery");
  }, [projectId, projectQuery.isLoading, projects, router, searchKey, tab]);
  return <LoadingState title="Opening cluster delivery" />;
}

export function useDeliveryPageIndex(parameter = "page") {
  const pathname = usePathname();
  const search = useSearchParams();
  const router = useRouter();
  const parsed = Number(search.get(parameter) ?? "0");
  const pageIndex = Number.isInteger(parsed) && parsed >= 0 ? parsed : 0;
  const setPageIndex = (nextPage: number) => {
    const next = new URLSearchParams(search);
    if (nextPage > 0) next.set(parameter, String(nextPage));
    else next.delete(parameter);
    router.replace(`${pathname}${next.size ? `?${next.toString()}` : ""}`);
  };
  return [pageIndex, setPageIndex] as const;
}

/**
 * React Table needs a row count to enable its next-page control. Most delivery
 * endpoints return an exact total, but append-only bundle-version history can
 * intentionally return `totalKnown: false`. In that case expose only the
 * smallest count proven by the current page and its next link; this enables
 * one safe server fetch without pretending the browser knows the full total.
 */
export function deliveryPageRowCount(
  page:
    | {
        data: unknown[];
        count: number;
        next: string | null;
        totalKnown: boolean;
      }
    | undefined,
  pageIndex: number,
  pageSize: number,
): number {
  if (!page) return 0;
  if (page.totalKnown) return page.count;
  const observed = pageIndex * pageSize + page.data.length;
  return observed + (page.next ? 1 : 0);
}

export function deliveryProjectLabel(
  project: { displayName: string; name: string; clusterId?: string },
  clusterNames: Map<string, string>,
) {
  const clusterName = project.clusterId
    ? clusterNames.get(project.clusterId)
    : undefined;
  return clusterName || project.displayName || project.name;
}

export function DeliveryShell({
  projectId,
  projects,
  setProjectId,
  showProjectSelect = true,
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
  const clusters = useClusters({ pageSize: 200 });
  const clusterNames = useMemo(() => {
    const names = new Map<string, string>();
    for (const cluster of clusters.data?.data ?? []) {
      if (cluster.id && (cluster.displayName || cluster.name)) {
        names.set(cluster.id, cluster.displayName || cluster.name);
      }
    }
    return names;
  }, [clusters.data?.data]);
  // Cluster delivery layout already owns the tab strip. Detail pages that
  // still sit on /dashboard/delivery/... only need a way back to the fleet.
  if (clusterId) return children;
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-center sm:justify-between">
        <Link
          href="/dashboard/delivery"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to delivery fleet
        </Link>
        {showProjectSelect ? (
          <label className="flex min-w-64 items-center gap-2 text-sm">
            <FolderKanban
              className="h-4 w-4 text-muted-foreground"
              aria-hidden="true"
            />
            <span className="sr-only">Delivery project</span>
            <select
              aria-label="Delivery project"
              value={projectId}
              onChange={(event) => setProjectId(event.target.value)}
              className="h-9 flex-1 rounded-md border border-border bg-background px-3 text-sm"
            >
              <option value="">Select a project</option>
              {projects.map((project) => (
                <option key={project.id} value={project.id}>
                  {deliveryProjectLabel(project, clusterNames)}
                </option>
              ))}
            </select>
          </label>
        ) : null}
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
  if (!allowed) return <PermissionState permission={permission} />;
  if (projectsCount === 0) {
    return (
      <EmptyState
        icon={PackageOpen}
        title="No projects available"
        description="Create or request access to a project before configuring delivery."
      />
    );
  }
  if (!projectId) {
    return (
      <EmptyState
        icon={FolderKanban}
        title="Choose a project"
        description="Delivery resources are isolated by project. Select one above to continue."
      />
    );
  }
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
  "h-9 w-full rounded-md border border-border bg-background px-3 text-sm focus:outline-none focus:ring-2 focus:ring-ring";
export const textareaClass =
  "min-h-24 w-full rounded-md border border-border bg-background px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-ring";
