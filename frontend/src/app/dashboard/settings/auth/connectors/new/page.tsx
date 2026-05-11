'use client';

/**
 * /dashboard/settings/auth/connectors/new/ — three-step connector wizard.
 *
 *   1. Type picker — card grid sourced from /connector-types/. Anything in
 *      the registry shows up automatically; new types only need a one-line
 *      `CONNECTOR_META` entry to get a friendly label/icon.
 *   2. Schema-driven form — `ConnectorForm` renders required + optional
 *      fields from the chosen type's spec.
 *   3. Apply prompt — after the connector is created we surface the option
 *      to immediately call /apply/ so the rendered ConfigMap goes out and
 *      Dex hot-reloads. Operators who want to batch multiple changes can
 *      skip and apply from the overview page.
 */
import { useState } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { ArrowLeft, Loader2, Search } from 'lucide-react';
import { cn } from '@/lib/utils';
import {
  useDexConnectorTypes,
  useCreateDexConnector,
  useApplyDexConfig,
} from '@/components/auth/hooks';
import { ConnectorForm } from '@/components/auth/connector-form';
import { getConnectorMeta } from '@/components/auth/connector-meta';
import type { DexConnectorTypeSpec } from '@/types';

type WizardStep = 'pick' | 'configure' | 'apply';

export default function NewConnectorPage() {
  const router = useRouter();
  const { data: types = [], isLoading: typesLoading } = useDexConnectorTypes();
  const createMutation = useCreateDexConnector();
  const applyMutation = useApplyDexConfig();

  const [step, setStep] = useState<WizardStep>('pick');
  const [selectedType, setSelectedType] = useState<DexConnectorTypeSpec | null>(null);
  const [search, setSearch] = useState('');
  const [serverError, setServerError] = useState<string | null>(null);
  const [createdId, setCreatedId] = useState<string | null>(null);

  const filtered = types.filter((t) => {
    if (!search) return true;
    const meta = getConnectorMeta(t.type);
    const haystack = `${t.type} ${meta.label} ${meta.description}`.toLowerCase();
    return haystack.includes(search.toLowerCase());
  });

  const handleSubmit = async (state: { name: string; displayName: string; config: Record<string, unknown>; enabled: boolean }) => {
    if (!selectedType) return;
    setServerError(null);
    try {
      const created = await createMutation.mutateAsync({
        type: selectedType.type,
        name: state.name,
        displayName: state.displayName,
        config: state.config,
        enabled: state.enabled,
      });
      setCreatedId(created.id);
      setStep('apply');
    } catch (err) {
      // Surface the server's `error.message` so missing-fields lists land
      // inline next to the form.
      const message = extractAxiosError(err) ?? 'Failed to create connector.';
      setServerError(message);
    }
  };

  return (
    <div className="max-w-3xl mx-auto space-y-6">
      <Link
        href="/dashboard/settings/auth"
        className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to Auth
      </Link>

      <div>
        <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
          Auth · New Connector
        </p>
        <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
          {step === 'pick'
            ? 'Choose a connector type'
            : step === 'configure'
              ? `Configure ${getConnectorMeta(selectedType?.type ?? '').label || selectedType?.type}`
              : 'Apply to Dex?'}
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
              placeholder="Search connector types…"
              className="flex-1 h-10 bg-transparent text-sm placeholder:text-muted-foreground focus:outline-none"
            />
          </div>

          {typesLoading ? (
            <div className="flex items-center justify-center h-32">
              <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : (
            <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
              {filtered.map((t) => {
                const meta = getConnectorMeta(t.type);
                const Icon = meta.icon;
                return (
                  <button
                    type="button"
                    key={t.type}
                    onClick={() => {
                      setSelectedType(t);
                      setStep('configure');
                    }}
                    className={cn(
                      'flex flex-col gap-2 p-4 rounded-lg border border-border bg-card text-left',
                      'hover:bg-card/80 hover:border-foreground/20 transition-colors'
                    )}
                  >
                    <div className="flex items-center gap-2">
                      <div className="flex-shrink-0 w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
                        <Icon className="h-4 w-4 text-foreground" />
                      </div>
                      <div className="min-w-0">
                        <p className="text-sm font-medium text-foreground truncate">{meta.label || t.type}</p>
                        <p className="text-2xs font-mono text-muted-foreground truncate">{t.type}</p>
                      </div>
                    </div>
                    <p className="text-xs text-muted-foreground line-clamp-2">
                      {meta.description || t.displayHint}
                    </p>
                    <div className="flex items-center justify-between mt-auto pt-1">
                      <span className="text-2xs text-muted-foreground">
                        {t.required.length} required field{t.required.length === 1 ? '' : 's'}
                      </span>
                    </div>
                  </button>
                );
              })}
            </div>
          )}
        </>
      )}

      {step === 'configure' && selectedType && (
        <div className="rounded-xl border border-border bg-card p-6">
          <ConnectorForm
            spec={selectedType}
            onSubmit={handleSubmit}
            submitLabel="Create connector"
            submitting={createMutation.isPending}
            onCancel={() => {
              setSelectedType(null);
              setServerError(null);
              setStep('pick');
            }}
            serverError={serverError}
          />
        </div>
      )}

      {step === 'apply' && createdId && (
        <div className="rounded-xl border border-border bg-card p-6 space-y-4">
          <div>
            <p className="text-sm font-medium text-foreground">Connector created.</p>
            <p className="text-xs text-muted-foreground mt-0.5">
              The new connector is saved but Dex hasn&apos;t reloaded yet. Apply now to push
              the rendered ConfigMap to your cluster, or batch with other changes from the
              overview page.
            </p>
          </div>
          <div className="flex flex-col sm:flex-row gap-2 sm:items-center sm:justify-end">
            <button
              type="button"
              onClick={() => router.push('/dashboard/settings/auth')}
              className="h-9 px-4 rounded-lg border border-border text-sm font-medium
                text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
            >
              Apply later
            </button>
            <button
              type="button"
              onClick={async () => {
                try {
                  await applyMutation.mutateAsync();
                  router.push('/dashboard/settings/auth');
                } catch {
                  /* mutation toasts on error */
                }
              }}
              disabled={applyMutation.isPending}
              className="inline-flex items-center justify-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground
                text-sm font-medium hover:opacity-90 transition-opacity disabled:opacity-50"
            >
              {applyMutation.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Apply to Dex now
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

// Pull the `error.message` out of an axios failure so the form can show the
// server's "missing required fields: ..." string verbatim. Falls back to
// `Error.message` when the response shape doesn't match.
function extractAxiosError(err: unknown): string | null {
  if (!err) return null;
  type ResponseShape = { response?: { data?: { error?: { message?: string }; message?: string } }; message?: string };
  const obj = err as ResponseShape;
  return (
    obj.response?.data?.error?.message ??
    obj.response?.data?.message ??
    obj.message ??
    null
  );
}
