import { createFileRoute } from "@tanstack/react-router";
/**
 * Project detail Overview tab — the bare /projects/[id] route.
 *
 * Surfaces the project's clusters, namespaces, and members. Namespace
 * assignment is editable here (it is the project's tenancy boundary — see
 * ProjectNamespacesCard); the Policy / Cloud Credentials / Quota tabs handle
 * the rest of the editable surfaces.
 */
import { Link as RouterLink } from "@tanstack/react-router";

import { Users, Server, Layers } from "lucide-react";
import { useProject } from "@/lib/hooks/projects";
import { useProjectRoleBindings } from "@/lib/hooks/rbac";
import { useCurrentUser } from "@/lib/hooks/auth";
import { canAssignProjectNamespaces } from "@/components/projects/hooks";
import { ProjectNamespacesCard } from "@/components/projects/namespaces-card";
import {
  ProjectMembersCard,
  PROJECT_MEMBERS_CARD_ID,
} from "@/components/projects/members-card";
import { formatRelativeTime } from "@/lib/utils";
import { WidgetGrid } from "@/components/dashboards/widget-grid";
import { QueryStates } from "@/components/ui/query-states";
import { MetricCard } from "@/components/ui/metric-card";
import { renderForProject } from "@/lib/api/dashboards";

function ProjectOverviewPage() {
  const params = Route.useParams();
  const id = params.id;
  const projectQuery = useProject(id);
  const { data: project, isLoading } = projectQuery;
  const { data: user } = useCurrentUser();
  const canEdit = canAssignProjectNamespaces(user);
  // `Project.members` is a dead wire field (never populated by the GET) — the
  // real roster is the project-scoped RBAC binding list; see members-card.tsx.
  const { data: memberBindings } = useProjectRoleBindings({ project_id: id });
  const memberCount = memberBindings?.length ?? 0;

  if (isLoading || projectQuery.isError) {
    return (
      <QueryStates
        query={projectQuery}
        permission="projects:read"
        notFound={<p className="text-sm text-muted-foreground">Project not found.</p>}
      >
        {() => null}
      </QueryStates>
    );
  }
  if (!project) {
    return <p className="text-sm text-muted-foreground">Project not found.</p>;
  }

  return (
    <div className="space-y-6">
      {/* Custom dashboard widgets (migration 058). Per-project scope —
          empty by default so the project overview stays clean unless
          the operator explicitly pins something here. */}
      {project?.id ? (
        <section className="space-y-2">
          <h3 className="text-sm font-medium text-muted-foreground uppercase tracking-wide">
            Widgets
          </h3>
          <WidgetGrid
            fetcher={() => renderForProject(project.id)}
            emptyHint=""
          />
        </section>
      ) : null}

      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <MetricCard
          dense
          icon={<Server className="h-3.5 w-3.5" />}
          label="Clusters"
          value={
            (project.clusterIds?.length ?? (project.clusterId ? 1 : 0)) || 1
          }
        />
        <MetricCard
          dense
          icon={<Layers className="h-3.5 w-3.5" />}
          label="Namespaces"
          value={project.namespaces?.length ?? 0}
        />
        {/* A plain in-page anchor rather than MetricCard's router-`Link` href
            (which resolves `to` as a route path, not a hash fragment) — this
            scrolls to the members card below instead of trying to navigate. */}
        <a href={`#${PROJECT_MEMBERS_CARD_ID}`} className="block">
          <MetricCard
            dense
            icon={<Users className="h-3.5 w-3.5" />}
            label="Members"
            value={memberCount}
          />
        </a>

        <div className="md:col-span-3">
          <ProjectNamespacesCard
            projectId={project.id}
            namespaces={project.namespaces ?? []}
            canEdit={canEdit}
          />
        </div>

        <div className="md:col-span-3">
          <ProjectMembersCard projectId={project.id} />
        </div>

        <div className="md:col-span-3 rounded-xl border border-border bg-card p-5 space-y-2">
          <h3 className="text-sm font-medium text-foreground">Identifiers</h3>
          <dl className="grid grid-cols-2 gap-x-6 gap-y-1.5 text-sm">
            <dt className="text-muted-foreground">Name</dt>
            <dd className="font-mono text-xs text-foreground">
              {project.name}
            </dd>
            <dt className="text-muted-foreground">Project ID</dt>
            <dd className="font-mono text-xs text-foreground">{project.id}</dd>
            <dt className="text-muted-foreground">Created</dt>
            <dd className="text-foreground">
              {formatRelativeTime(project.createdAt)}
            </dd>
            <dt className="text-muted-foreground">Updated</dt>
            <dd className="text-foreground">
              {formatRelativeTime(project.updatedAt)}
            </dd>
          </dl>
          <p className="text-xs text-muted-foreground pt-2">
            Configure pod security and resource limits on the{" "}
            <RouterLink
              to="/dashboard/projects/$id/policy" params={{ id: project.id }}
              className="text-foreground underline-offset-2 hover:underline"
            >
              Policy tab
            </RouterLink>
            .
          </p>
        </div>
      </div>
    </div>
  );
}

export const Route = createFileRoute("/dashboard/projects/$id/")({
  component: ProjectOverviewPage,
});
