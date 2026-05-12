'use client';

/**
 * Cluster Templates · New.
 *
 * Pure form page — there's no provider-picker step like the cloud-credentials
 * wizard, so we render the form directly inside the header card. Write
 * access is gated on `cluster_templates:write`; the form itself stays
 * mounted in read-only-via-disabled-submit mode if the user lacks the role
 * (so deep-links don't 404), but the Save button refuses to fire.
 */
import { useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { ArrowLeft, AlertCircle } from 'lucide-react';
import { useCurrentUser } from '@/lib/hooks';
import {
  useCreateClusterTemplate,
  canWriteClusterTemplates,
} from '@/components/projects/hooks';
import { TemplateForm } from '@/components/projects/cluster-templates/template-form';

export default function NewClusterTemplatePage() {
  const router = useRouter();
  const { data: user } = useCurrentUser();
  const canWrite = canWriteClusterTemplates(user);
  const createMutation = useCreateClusterTemplate();
  const [serverError, setServerError] = useState<string | null>(null);

  return (
    <div className="max-w-4xl mx-auto space-y-6">
      <Link
        href="/dashboard/cluster-templates"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to templates
      </Link>

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Cluster Templates · New
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
          Create a template
        </h1>
        <p className="text-sm text-muted-foreground mt-1">
          Capture environment, labels, tools, project defaults, and registration policy.
        </p>
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
        submitting={createMutation.isPending}
        serverError={serverError}
        onCancel={() => router.push('/dashboard/cluster-templates')}
        onSubmit={async (body) => {
          if (!canWrite) {
            setServerError('You do not have permission to create cluster templates.');
            return;
          }
          setServerError(null);
          try {
            const created = await createMutation.mutateAsync(body);
            router.push(`/dashboard/cluster-templates/${created.id}`);
          } catch (err) {
            const msg = extractAxiosError(err) ?? 'Failed to create template.';
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
