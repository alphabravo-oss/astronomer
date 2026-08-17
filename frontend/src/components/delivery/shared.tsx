import { useEffect, useMemo, type ReactNode } from "react";
import { Link } from "@/lib/link";
import { usePathname, useRouter, useSearchParams } from "@/lib/navigation";
import { useProjects } from "@/lib/hooks";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  EmptyState,
  ErrorState,
  LoadingState,
  PermissionState,
} from "@/components/ui/empty-state";
import { FolderKanban, PackageOpen } from "lucide-react";
import { cn } from "@/lib/utils";

const deliveryTabs = [
  ["Overview", "/dashboard/delivery"],
  ["Sources", "/dashboard/delivery/sources"],
  ["Bundles", "/dashboard/delivery/bundles"],
  ["Targets", "/dashboard/delivery/targets"],
  ["Rollouts", "/dashboard/delivery/rollouts"],
  ["Deployments", "/dashboard/delivery/deployments"],
] as const;

export function useDeliveryProjectScope() {
  const projects = useProjects({ pageSize: 200 });
  const pathname = usePathname();
  const search = useSearchParams();
  const router = useRouter();
  const rows = useMemo(() => projects.data?.data ?? [], [projects.data?.data]);
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

export function DeliveryShell({
  projectId,
  projects,
  setProjectId,
  children,
}: {
  projectId: string;
  projects: Array<{ id: string; displayName: string; name: string }>;
  setProjectId: (id: string) => void;
  children: ReactNode;
}) {
  const pathname = usePathname();
  return (
    <div className="space-y-6">
      <div className="flex flex-col gap-4 border-b border-border pb-4 lg:flex-row lg:items-end lg:justify-between">
        <nav aria-label="Continuous Delivery" className="flex flex-wrap gap-1">
          {deliveryTabs.map(([label, href]) => {
            const active =
              pathname === href ||
              (href !== "/dashboard/delivery" &&
                pathname.startsWith(`${href}/`));
            return (
              <Link
                key={href}
                href={`${href}${projectId ? `?project=${encodeURIComponent(projectId)}` : ""}`}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "rounded-md px-3 py-2 text-sm font-medium transition-colors",
                  active
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:bg-accent hover:text-foreground",
                )}
              >
                {label}
              </Link>
            );
          })}
        </nav>
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
                {project.displayName || project.name}
              </option>
            ))}
          </select>
        </label>
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
        ].includes(normalized)
      ? "failed"
      : [
            "degraded",
            "blocked",
            "paused",
            "timed_out",
            "upgrade_required",
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
