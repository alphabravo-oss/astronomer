'use client';

/**
 * Cluster Templates · Edit — preload the existing template into the same
 * form used by the create page.
 */
import { use, useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Loader2, AlertCircle } from 'lucide-react';
import { useCurrentUser } from '@/lib/hooks';
import {
  useClusterTemplate,
  useUpdateClusterTemplate,
  canWriteClusterTemplates,
} from '@/components/projects/hooks';
import { TemplateForm } from '@/components/projects/cluster-templates/template-form';

interface EditPageProps {
  params: Promise<{ id: string }>;
}

export default function ClusterTemplateEditPage({ params }: EditPageProps) {
  const { id } = use(params);
  const router = useRouter();
  const { data: user } = useCurrentUser();
  const canWrite = canWriteClusterTemplates(user);

  const { data: template, isLoading } = useClusterTemplate(id);
  const updateMutation = useUpdateClusterTemplate();
  const [serverError, setServerError] = useState<string | null>(null);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!template) {
    return <p className="text-sm text-muted-foreground">Template not found.</p>;
  }

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <Link
        href={`/dashboard/cluster-templates/${id}`}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to template
      </Link>

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Cluster Templates · Edit
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
          {template.displayName}
        </h1>
      </div>

      {!canWrite && (
        <div className="flex items-start gap-2 rounded-lg border border-border bg-muted/30 p-3 text-xs text-muted-foreground">
          <AlertCircle className="h-4 w-4 mt-0.5 flex-shrink-0" />
          <p>
            Saving requires the <span className="font-mono">cluster_templates:write</span> role.
          </p>
        </div>
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
            const msg = extractAxiosError(err) ?? 'Failed to update template.';
            setServerError(msg);
          }
        }}
      />
    </div>
  );
}

function extractAxiosError(err: unknown): string | null {
  if (!err) return null;
  type ResponseShape = {
    response?: { data?: { error?: { message?: string }; message?: string } };
    message?: string;
  };
  const obj = err as ResponseShape;
  return (
    obj.response?.data?.error?.message ??
    obj.response?.data?.message ??
    obj.message ??
    null
  );
}
