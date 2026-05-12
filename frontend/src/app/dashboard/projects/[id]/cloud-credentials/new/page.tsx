'use client';

/**
 * Project · Cloud Credentials · New — three-step wizard.
 *
 * 1. Pick a provider (card grid sourced from /cloud-credentials/providers/,
 *    same style as the Dex connector wizard).
 * 2. Render the schema-driven `CredentialForm` for the chosen provider.
 * 3. Submit → redirect back to the list with a success toast.
 *
 * Target refs are part of step 2 (rather than a third step) because they
 * don't depend on the provider — the form already groups them visually
 * at the bottom.
 */
import { useMemo, useState } from 'react';
import Link from 'next/link';
import { use } from 'react';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Loader2, Search, Cloud } from 'lucide-react';
import {
  useCloudCredentialProviders,
  useCreateCloudCredential,
} from '@/components/projects/hooks';
import { CredentialForm } from '@/components/projects/cloud-credentials/credential-form';
import { ProviderBadge } from '@/components/projects/cloud-credentials/provider-badge';
import type { CloudCredentialProviderSpec } from '@/lib/api/project-detail';
import { cn } from '@/lib/utils';

interface NewPageProps {
  params: Promise<{ id: string }>;
}

export default function NewCloudCredentialPage({ params }: NewPageProps) {
  const { id: projectId } = use(params);
  const router = useRouter();
  const { data: providers = [], isLoading } = useCloudCredentialProviders();
  const createMutation = useCreateCloudCredential(projectId);

  const [step, setStep] = useState<'pick' | 'configure'>('pick');
  const [selected, setSelected] = useState<CloudCredentialProviderSpec | null>(null);
  const [search, setSearch] = useState('');
  const [serverError, setServerError] = useState<string | null>(null);

  const filtered = useMemo(() => {
    if (!search) return providers;
    const q = search.toLowerCase();
    return providers.filter(
      (p) =>
        p.provider.toLowerCase().includes(q) ||
        p.displayName.toLowerCase().includes(q) ||
        (p.description?.toLowerCase().includes(q) ?? false),
    );
  }, [providers, search]);

  const backToList = `/dashboard/projects/${projectId}/cloud-credentials`;

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
          Cloud Credentials · New
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
          {step === 'pick'
            ? 'Choose a provider'
            : `Configure ${selected?.displayName || selected?.provider}`}
        </h1>
      </div>

      {step === 'pick' && (
        <>
          <div className="flex items-center gap-2 px-3 rounded-lg border border-border bg-background">
            <Search className="h-4 w-4 text-muted-foreground" />
            <input
              type="text"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Search providers…"
              className="flex-1 h-10 bg-transparent text-sm placeholder:text-muted-foreground focus:outline-none"
            />
          </div>

          {isLoading ? (
            <div className="flex items-center justify-center h-32">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {filtered.map((p) => (
                <button
                  type="button"
                  key={p.provider}
                  onClick={() => {
                    setSelected(p);
                    setStep('configure');
                  }}
                  className={cn(
                    'flex flex-col gap-2 p-4 rounded-lg border border-border bg-card text-left',
                    'hover:bg-card/80 hover:border-foreground/20 transition-colors',
                  )}
                >
                  <div className="flex items-center gap-2">
                    <div className="flex-shrink-0 w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
                      <Cloud className="h-4 w-4 text-foreground" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <p className="text-sm font-medium text-foreground truncate">
                        {p.displayName}
                      </p>
                      <ProviderBadge provider={p.provider} />
                    </div>
                  </div>
                  {p.description && (
                    <p className="text-xs text-muted-foreground line-clamp-2">{p.description}</p>
                  )}
                  <p className="text-2xs text-muted-foreground mt-auto pt-1">
                    {p.fields.filter((f) => f.required).length} required field
                    {p.fields.filter((f) => f.required).length === 1 ? '' : 's'}
                  </p>
                </button>
              ))}
              {filtered.length === 0 && (
                <p className="col-span-full text-xs text-muted-foreground text-center py-6">
                  No providers match.
                </p>
              )}
            </div>
          )}
        </>
      )}

      {step === 'configure' && selected && (
        <div className="rounded-xl border border-border bg-card p-6">
          <CredentialForm
            provider={selected.provider}
            spec={selected}
            submitting={createMutation.isPending}
            serverError={serverError}
            onCancel={() => {
              setSelected(null);
              setServerError(null);
              setStep('pick');
            }}
            onSubmit={async (body) => {
              setServerError(null);
              try {
                await createMutation.mutateAsync(body);
                router.push(backToList);
              } catch (err) {
                const msg = extractAxiosError(err) ?? 'Failed to create credential.';
                setServerError(msg);
              }
            }}
          />
        </div>
      )}
    </div>
  );
}

// Same shape used by the Dex wizard: surface the server's
// `error.message` so missing-fields lists land inline.
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
