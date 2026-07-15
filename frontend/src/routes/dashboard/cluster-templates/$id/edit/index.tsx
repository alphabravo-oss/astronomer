import { createFileRoute } from '@tanstack/react-router';
/**
 * Cluster Templates · Edit — preload the existing template into the same
 * form used by the create page.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useParams, useRouter } from '@/lib/navigation';
import { ArrowLeft } from 'lucide-react';
import { ErrorState, LoadingState, PermissionState } from '@/components/ui/empty-state';
import { extractApiErrorMessage } from '@/lib/api/errors';
import { useCurrentUser } from '@/lib/hooks';
import {
  useClusterTemplate,
  useUpdateClusterTemplate,
  canWriteClusterTemplates,
} from '@/components/projects/hooks';
import { TemplateForm } from '@/components/projects/cluster-templates/template-form';

function ClusterTemplateEditPage() {
  const params = useParams();
  const id = params.id as string;
  const router = useRouter();
  const { data: user } = useCurrentUser();
  const canWrite = canWriteClusterTemplates(user);

  const { data: template, isLoading } = useClusterTemplate(id);
  const updateMutation = useUpdateClusterTemplate();
  const [serverError, setServerError] = useState<string | null>(null);

  if (isLoading) {
    return <LoadingState title="Loading cluster template" className="h-32 py-0" />;
  }
  if (!template) {
    return <ErrorState title="Template not found" description="The requested cluster template does not exist or is no longer available." />;
  }

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <Link
        href={`/dashboard/cluster-templates/${id}`}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to bundle
      </Link>

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Onboarding Bundles · Edit
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
          {template.displayName}
        </h1>
      </div>

      {!canWrite && (
        <PermissionState
          title="Write permission required"
          permission="cluster_templates:write"
          description={<>Saving requires the <span className="font-mono">cluster_templates:write</span> role.</>}
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
        onCancel={() => router.push(`/dashboard/cluster-templates/${id}`)}
        onSubmit={async (body) => {
          if (!canWrite) {
            setServerError('You do not have permission to update cluster templates.');
            return;
          }
          setServerError(null);
          try {
            await updateMutation.mutateAsync({ id, body });
            router.push(`/dashboard/cluster-templates/${id}`);
          } catch (err) {
            const msg = extractApiErrorMessage(err) ?? 'Failed to update template.';
            setServerError(msg);
          }
        }}
      />
    </div>
  );
}

export const Route = createFileRoute('/dashboard/cluster-templates/$id/edit/')({
  component: ClusterTemplateEditPage,
});
