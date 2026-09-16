import { createFileRoute } from "@tanstack/react-router";
/**
 * Cluster Templates · Edit — preload the existing template into the same
 * form used by the create page.
 */
import { useState } from "react";
import { Link as RouterLink } from "@tanstack/react-router";
import { useNavigate } from "@tanstack/react-router";
import { ArrowLeft } from "lucide-react";
import { PermissionState, StatePanel } from "@/components/ui/empty-state";
import { QueryStates } from "@/components/ui/query-states";
import { extractApiErrorMessage } from "@/lib/api/errors";
import { useCurrentUser } from "@/lib/hooks/auth";
import {
  useClusterTemplate,
  useUpdateClusterTemplate,
  canWriteClusterTemplates,
} from "@/components/projects/hooks";
import { TemplateForm } from "@/components/projects/cluster-templates/template-form";
import { PageHeader, PageShell } from "@/components/ui/page";

function ClusterTemplateEditPage() {
  const params = Route.useParams();
  const id = params.id;
  const navigate = useNavigate();
  const { data: user } = useCurrentUser();
  const canWrite = canWriteClusterTemplates(user);

  const templateQuery = useClusterTemplate(id);
  const updateMutation = useUpdateClusterTemplate();
  const [serverError, setServerError] = useState<string | null>(null);

  if (
    templateQuery.isLoading ||
    templateQuery.isError ||
    templateQuery.data === undefined
  ) {
    return (
      <QueryStates
        query={templateQuery}
        loadingTitle="Loading onboarding bundle"
        permission="cluster_templates:read"
        errorTitle="Failed to load onboarding bundle"
        notFound={
          <StatePanel
            title="Bundle not found"
            description="The onboarding bundle may have been deleted or is outside your access scope."
            actionLabel="Back to bundles"
            actionHref="/dashboard/cluster-templates"
          />
        }
      >
        {null}
      </QueryStates>
    );
  }
  const template = templateQuery.data;

  return (
    <PageShell>
      <RouterLink
        to="/dashboard/cluster-templates/$id" params={{ id }}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to bundle
      </RouterLink>

      <PageHeader
        eyebrow="Onboarding Bundles · Edit"
        title={template.displayName}
      />

      {!canWrite && (
        <PermissionState
          title="Write permission required"
          permission="cluster_templates:write"
          description={
            <>
              Saving requires the{" "}
              <span className="font-mono">cluster_templates:write</span> role.
            </>
          }
          className="rounded-lg border border-border bg-muted/30 p-6"
        />
      )}

      <TemplateForm
        isEdit
        submitting={updateMutation.isPending}
        serverError={serverError}
        initial={{
          name: template.name,
          displayName: template.displayName,
          description: template.description,
          spec: template.spec,
        }}
        onCancel={() => void navigate({ to: `/dashboard/cluster-templates/${id}` })}
        onSubmit={async (body) => {
          if (!canWrite) {
            setServerError(
              "You do not have permission to update cluster templates.",
            );
            return;
          }
          setServerError(null);
          try {
            await updateMutation.mutateAsync({ id, body });
            void navigate({ to: `/dashboard/cluster-templates/${id}` });
          } catch (err) {
            const msg =
              extractApiErrorMessage(err) ?? "Failed to update template.";
            setServerError(msg);
          }
        }}
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/cluster-templates/$id/edit/")({
  component: ClusterTemplateEditPage,
});
