import { createFileRoute, Outlet } from "@tanstack/react-router";
import { Link } from "@/lib/link";
import { useParams, usePathname } from "@/lib/navigation";
import {
  ArrowLeft,
  Boxes,
  Crosshair,
  GitBranch,
  Layers,
  Radio,
  Route as RouteIcon,
} from "lucide-react";
import { useCluster } from "@/lib/hooks";
import { cn } from "@/lib/utils";
import { useDeliveryProjectScope } from "@/components/delivery/shared";

const tabs = [
  { key: "flux", label: "Flux", icon: Radio, segment: "" },
  { key: "deployments", label: "Deployments", icon: Layers, segment: "/deployments" },
  { key: "rollouts", label: "Rollouts", icon: RouteIcon, segment: "/rollouts" },
  { key: "sources", label: "Sources", icon: GitBranch, segment: "/sources" },
  { key: "bundles", label: "Bundles", icon: Boxes, segment: "/bundles" },
  { key: "targets", label: "Targets", icon: Crosshair, segment: "/targets" },
] as const;

function ClusterDeliveryLayout() {
  const params = useParams();
  const clusterId = params.id as string;
  const pathname = usePathname();
  const { data: cluster } = useCluster(clusterId);
  const { projectId, projects, setProjectId } = useDeliveryProjectScope({
    clusterId,
  });
  const base = `/dashboard/clusters/${clusterId}/delivery`;
  const projectQuery = projectId
    ? `?project=${encodeURIComponent(projectId)}`
    : "";
  const remaining = pathname.startsWith(base) ? pathname.slice(base.length) : "";
  const activeKey =
    tabs
      .filter(
        (tab) =>
          tab.segment &&
          (remaining === tab.segment || remaining.startsWith(`${tab.segment}/`)),
      )
      .sort((a, b) => b.segment.length - a.segment.length)[0]?.key ?? "flux";

  return (
    <div className="space-y-6">
      <Link
        href="/dashboard/delivery"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to delivery fleet
      </Link>
      <div className="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div className="min-w-0">
          <p className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            Cluster delivery
          </p>
          <h1 className="mt-1 truncate text-2xl font-semibold tracking-tight text-foreground">
            {cluster?.displayName || cluster?.name || "Cluster"}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Flux and delivery for this environment. Switch clusters from the
            sidebar to stay on the same tab.
          </p>
        </div>
        {projects.length > 1 ? (
          <label className="flex min-w-56 items-center gap-2 text-sm">
            <span className="text-muted-foreground">Project</span>
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
        ) : null}
      </div>
      <div className="border-b border-border">
        <nav aria-label="Cluster delivery" className="flex flex-wrap gap-4">
          {tabs.map((tab) => {
            const Icon = tab.icon;
            const href = `${base}${tab.segment}${projectQuery}`;
            const active = activeKey === tab.key;
            return (
              <Link
                key={tab.key}
                href={href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex items-center gap-2 border-b-2 pb-3 text-sm font-medium transition-colors",
                  active
                    ? "border-foreground text-foreground"
                    : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                <Icon className="h-4 w-4" />
                {tab.label}
              </Link>
            );
          })}
        </nav>
      </div>
      <Outlet />
    </div>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/delivery")({
  component: ClusterDeliveryLayout,
});
