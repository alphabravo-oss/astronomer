import { createFileRoute } from '@tanstack/react-router';
/**
 * /dashboard/settings/auth/connectors/[id]/ — edit a single connector.
 *
 * Shares the schema-driven form with the `/new` wizard. The differences:
 *   - `name` is rendered read-only (it's the Dex connector id).
 *   - Secret fields come back as empty strings + a `__<key>_set` flag; the
 *     form renders them as masked placeholders and only sends new values
 *     when the user actually types into them, so we never leak ciphertext
 *     and we don't accidentally clobber stored secrets on a no-op save.
 */
import { useState } from 'react';
import { Link } from '@/lib/link';
import { useParams, useRouter } from '@/lib/navigation';
import { ArrowLeft, Loader2, Trash2 } from 'lucide-react';
import { extractApiErrorMessage } from '@/lib/api/errors';
import {
  useDexConnector,
  useDexConnectorTypes,
  useUpdateDexConnector,
  useDeleteDexConnector,
  useApplyDexConfig,
} from '@/components/auth/hooks';
import { ConnectorForm, type ConnectorFormState } from '@/components/auth/connector-form';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { getConnectorMeta } from '@/components/auth/connector-meta';

function EditConnectorPage() {
  const params = useParams();
  const id = String(params?.id ?? '');
  const router = useRouter();
  const { data: connector, isLoading } = useDexConnector(id);
  const { data: types = [] } = useDexConnectorTypes();
  const updateMutation = useUpdateDexConnector();
  const deleteMutation = useDeleteDexConnector();
  const applyMutation = useApplyDexConfig();

  const [serverError, setServerError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (isLoading) {
    return (
      <div className="flex items-center justify-center h-48">
        <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!connector) {
    return (
      <div className="max-w-2xl mx-auto text-center py-12 space-y-3">
        <p className="text-sm text-foreground">Connector not found.</p>
        <Link
          href="/dashboard/settings/auth"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Auth
        </Link>
      </div>
    );
  }

  const spec = types.find((t) => t.type === connector.type);
  const meta = getConnectorMeta(connector.type);

  const handleSubmit = async (state: ConnectorFormState) => {
    setServerError(null);
    try {
      await updateMutation.mutateAsync({
        id: connector.id,
        data: {
          // Type and name are immutable in the UI — Dex's connector id is the
          // name, and changing the type would invalidate the config schema.
          displayName: state.displayName,
          config: state.config,
          enabled: state.enabled,
        },
      });
      // Stay on the page so the operator can immediately apply.
    } catch (err) {
      const message = extractApiErrorMessage(err) ?? 'Failed to update connector.';
      setServerError(message);
    }
  };

  const handleDelete = async () => {
    try {
      await deleteMutation.mutateAsync(connector.id);
      router.push('/dashboard/settings/auth');
    } catch {
      /* toast handles the error */
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

      <div className="flex items-start justify-between gap-4">
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Auth · Edit Connector
          </p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
            {meta.label || connector.type} · {connector.name}
          </h1>
          <p className="text-sm text-muted-foreground mt-1 font-mono">{connector.id}</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => applyMutation.mutate()}
            disabled={applyMutation.isPending}
            className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-border text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
          >
            {applyMutation.isPending ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : null}
            Apply to Dex
          </button>
          <button
            type="button"
            onClick={() => setConfirmDelete(true)}
            className="inline-flex items-center gap-2 h-9 px-3 rounded-lg border border-status-error/40 text-sm text-status-error
              hover:bg-status-error/10 transition-colors"
          >
            <Trash2 className="h-3.5 w-3.5" />
            Delete
          </button>
        </div>
      </div>

      <div className="rounded-xl border border-border bg-card p-6">
        {spec ? (
          <ConnectorForm
            spec={spec}
            initial={{
              name: connector.name,
              displayName: connector.displayName,
              config: connector.config,
              enabled: connector.enabled,
            }}
            onSubmit={handleSubmit}
            submitLabel="Save changes"
            submitting={updateMutation.isPending}
            serverError={serverError}
            isEdit
          />
        ) : (
          <p className="text-sm text-muted-foreground">
            Connector type <span className="font-mono">{connector.type}</span> is not in the
            registry. Edit via the API.
          </p>
        )}
      </div>

      <ConfirmDialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        onConfirm={handleDelete}
        title="Delete connector"
        description={`Remove the "${connector.name}" connector. Apply changes afterwards to roll out to Dex.`}
        confirmText="Delete"
        confirmValue={connector.name}
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}

export const Route = createFileRoute('/dashboard/settings/auth/connectors/$id/')({
  component: EditConnectorPage,
});
