'use client';

/**
 * Project · Cloud Credentials · Edit.
 *
 * Loads the existing credential, looks up its provider spec, and renders
 * the same `CredentialForm` used by the new-credential wizard with
 * `isEdit` set. Untouched secret fields are stripped on submit so the
 * backend keeps the existing ciphertext.
 */
import { use, useMemo, useState } from 'react';
import { Link } from '@/lib/link';
import { useRouter } from '@/lib/navigation';
import { ArrowLeft, Loader2 } from 'lucide-react';
import { extractApiErrorMessage } from '@/lib/api/errors';
import {
  useCloudCredentialProviders,
  useProjectCloudCredential,
  useUpdateCloudCredential,
} from '@/components/projects/hooks';
import { CredentialForm } from '@/components/projects/cloud-credentials/credential-form';
import { ProviderBadge } from '@/components/projects/cloud-credentials/provider-badge';

interface EditPageProps {
  params: Promise<{ id: string; credId: string }>;
}

export default function EditCloudCredentialPage({ params }: EditPageProps) {
  const { id: projectId, credId } = use(params);
  const router = useRouter();
  const { data: providers = [] } = useCloudCredentialProviders();
  const { data: credential, isLoading } = useProjectCloudCredential(projectId, credId);
  const updateMutation = useUpdateCloudCredential(projectId);
  const [serverError, setServerError] = useState<string | null>(null);

  const spec = useMemo(
    () => providers.find((p) => p.provider === credential?.provider),
    [providers, credential?.provider],
  );

  // The backend hides existing secret values and instead emits sentinel
  // `__<field>_set: true` flags. Strip them out of the form's initial config
  // and turn them into a fast lookup the form uses to render `<set>` hints.
  const { displayConfig, secretsSet } = useMemo(() => {
    if (!credential) return { displayConfig: {}, secretsSet: new Set<string>() };
    const cfg: Record<string, unknown> = {};
    const set = new Set<string>();
    for (const [k, v] of Object.entries(credential.config)) {
      if (k.startsWith('__') && k.endsWith('_set')) {
        if (v) set.add(k.slice(2, -4));
        continue;
      }
      cfg[k] = v;
    }
    return { displayConfig: cfg, secretsSet: set };
  }, [credential]);

  const backToList = `/dashboard/projects/${projectId}/cloud-credentials`;

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-32">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!credential || !spec) {
    return (
      <div className="space-y-4">
        <Link
          href={backToList}
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back
        </Link>
        <p className="text-sm text-muted-foreground">
          Credential or provider spec not found.
        </p>
      </div>
    );
  }

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <Link
        href={backToList}
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to credentials
      </Link>

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Cloud Credentials · Edit
        </p>
        <div className="flex items-center gap-2 mt-1">
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">
            {credential.name}
          </h1>
          <ProviderBadge provider={credential.provider} />
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6">
        <CredentialForm
          provider={credential.provider}
          spec={spec}
          isEdit
          submitting={updateMutation.isPending}
          serverError={serverError}
          initial={{
            name: credential.name,
            description: credential.description,
            config: displayConfig,
            targetRefs: credential.targetRefs,
            secretsSet,
          }}
          onCancel={() => router.push(backToList)}
          onSubmit={async (body) => {
            setServerError(null);
            try {
              await updateMutation.mutateAsync({ credentialId: credId, body });
              router.push(backToList);
            } catch (err) {
              const msg = extractApiErrorMessage(err) ?? 'Failed to update credential.';
              setServerError(msg);
            }
          }}
        />
      </div>
    </div>
  );
}
