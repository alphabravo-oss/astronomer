import { createFileRoute } from '@tanstack/react-router';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * /dashboard/settings/vault — admin CRUD over HashiCorp Vault
 * connections (migration 067).
 *
 * The page is superuser-gated by the standard SettingsAuthGate; the
 * backend re-checks superuser status on every request so a JWT alone
 * can't bypass.
 *
 * Auth-blob input UI varies per method:
 *   - token:      single textarea
 *   - approle:    role_id (visible) + secret_id (password input)
 *   - kubernetes: role (text) + jwt_path (text, defaults to in-cluster)
 *
 * The form submits auth values verbatim; on PUT, fields holding the
 * sentinel `<encrypted>` are preserved server-side so an edit doesn't
 * blank the stored secret.
 */
import { useState } from 'react';
import { useRouter } from '@/lib/navigation';
import { ArrowLeft, KeyRound, Plus, Trash2 } from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';

import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { useAppForm, useStore } from '@/lib/form';
import { queryKeys } from '@/lib/hooks';
import { extractApiErrorMessage } from '@/lib/api/errors';
import {
  listVaultConnections,
  createVaultConnection,
  deleteVaultConnection,
  testVaultConnection,
  VAULT_AUTH_SENTINEL,
  type VaultConnection,
  type VaultAuthMethod,
  type VaultConnectionWriteRequest,
} from '@/lib/api/vault';

function blankBody(method: VaultAuthMethod): VaultConnectionWriteRequest {
  const auth: Record<string, string> =
    method === 'token'
      ? { token: '' }
      : method === 'approle'
      ? { role_id: '', secret_id: '' }
      : { role: '', jwt_path: '/var/run/secrets/kubernetes.io/serviceaccount/token' };
  return {
    name: '',
    description: '',
    addr: 'https://',
    auth_method: method,
    auth,
    default_mount: 'secret',
    namespace: '',
    tls_skip_verify: false,
    ca_cert_pem: '',
    enabled: true,
  };
}

function VaultConnectionsPage() {
  const router = useRouter();
  const qc = useQueryClient();
  const { data: rows = [], isLoading } = useQuery({
    queryKey: queryKeys.vault.connections,
    queryFn: listVaultConnections,
  });

  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const createMu = useMutation({
    mutationFn: createVaultConnection,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.vault.connections });
    },
    onError: (err: unknown) => setError(extractApiErrorMessage(err) ?? String(err)),
  });

  const form = useAppForm({
    defaultValues: blankBody('token'),
    onSubmit: async ({ value }) => {
      try {
        await createMu.mutateAsync(value);
        setCreating(false);
        form.reset(blankBody('token'));
        setError(null);
      } catch {
        // onError above surfaces the message inline.
      }
    },
  });
  // The auth-blob inputs switch shape on the selected method.
  const authMethod = useStore(form.store, (s) => s.values.auth_method);
  const delMu = useMutation({
    mutationFn: deleteVaultConnection,
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.vault.connections }),
  });
  const testMu = useMutation({
    mutationFn: (id: string) => testVaultConnection(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: queryKeys.vault.connections }),
  });

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center gap-3">
        <button
          onClick={() => router.push('/dashboard/settings')}
          className="text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
        </button>
        <h1 className="text-2xl font-semibold flex items-center gap-2">
          <KeyRound className="h-5 w-5" /> Vault connections
        </h1>
        <button
          onClick={() => setCreating(true)}
          className="ml-auto inline-flex items-center gap-2 rounded bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
        >
          <Plus className="h-4 w-4" /> New connection
        </button>
      </div>

      <p className="text-sm text-muted-foreground max-w-3xl">
        Vault references in values blobs use the syntax{' '}
        <code className="bg-muted px-1 rounded">{'${vault://<connection>/<engine>/<path>#<key>}'}</code>.
        References are resolved in-memory at install time; the resolved value is never written to
        the database or audit log.
      </p>

      {isLoading ? (
        <div className="text-muted-foreground">Loading…</div>
      ) : (
        <Table className="w-full text-sm border border-border rounded">
          <TableHeader className="bg-muted text-left">
            <TableRow>
              <TableHead className="p-2">Name</TableHead>
              <TableHead className="p-2">Address</TableHead>
              <TableHead className="p-2">Auth</TableHead>
              <TableHead className="p-2">Health</TableHead>
              <TableHead className="p-2">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={5} className="p-4 text-muted-foreground">
                  No Vault connections configured yet.
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row: VaultConnection) => (
                <TableRow key={row.id} className="border-t border-border">
                  <TableCell className="p-2 font-medium">{row.name}</TableCell>
                  <TableCell className="p-2 font-mono text-xs">{row.addr}</TableCell>
                  <TableCell className="p-2 uppercase text-xs">{row.authMethod}</TableCell>
                  <TableCell className="p-2">
                    {row.lastHealthOk ? (
                      <span className="text-status-success">ok</span>
                    ) : row.lastError ? (
                      <span className="text-status-error" title={row.lastError}>
                        error
                      </span>
                    ) : (
                      <span className="text-muted-foreground">unchecked</span>
                    )}
                  </TableCell>
                  <TableCell className="p-2 flex gap-2">
                    <button
                      onClick={() => testMu.mutate(row.id)}
                      className="text-xs underline"
                    >
                      Test
                    </button>
                    <button
                      onClick={() => {
                        if (confirm(`Delete connection "${row.name}"?`)) delMu.mutate(row.id);
                      }}
                      className="text-xs text-status-error inline-flex items-center gap-1"
                    >
                      <Trash2 className="h-3 w-3" /> Delete
                    </button>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      )}

      {creating && (
        <form
          className="space-y-3 max-w-xl border border-border rounded p-4"
          onSubmit={(e) => {
            e.preventDefault();
            void form.handleSubmit();
          }}
        >
          <h2 className="font-medium">New connection</h2>
          {error && <div className="text-status-error text-sm">{error}</div>}
          <label className="block text-sm">
            Name
            <form.Field name="name">
              {(field) => (
                <input
                  required
                  className="block w-full bg-background border border-border rounded p-1.5 mt-1"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                />
              )}
            </form.Field>
          </label>
          <label className="block text-sm">
            Vault URL
            <form.Field name="addr">
              {(field) => (
                <input
                  required
                  className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                />
              )}
            </form.Field>
          </label>
          <label className="block text-sm">
            Auth method
            <select
              className="block w-full bg-background border border-border rounded p-1.5 mt-1"
              value={authMethod}
              onChange={(e) => {
                const method = e.target.value as VaultAuthMethod;
                // Switching methods resets the draft to that method's blank
                // shape, exactly like the old setDraft(blankBody(method)).
                form.reset(blankBody(method));
              }}
            >
              <option value="token">Token</option>
              <option value="approle">AppRole</option>
              <option value="kubernetes">Kubernetes</option>
            </select>
          </label>
          <form.Field name="auth">
            {(field) => (
              <>
                {authMethod === 'token' && (
                  <label className="block text-sm">
                    Token
                    <input
                      type="password"
                      required
                      className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                      value={field.state.value.token ?? ''}
                      onChange={(e) => field.handleChange({ token: e.target.value })}
                      onBlur={field.handleBlur}
                    />
                  </label>
                )}
                {authMethod === 'approle' && (
                  <>
                    <label className="block text-sm">
                      Role ID
                      <input
                        required
                        className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                        value={field.state.value.role_id ?? ''}
                        onChange={(e) =>
                          field.handleChange({ ...field.state.value, role_id: e.target.value })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                    <label className="block text-sm">
                      Secret ID
                      <input
                        type="password"
                        required
                        className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                        value={field.state.value.secret_id ?? ''}
                        onChange={(e) =>
                          field.handleChange({ ...field.state.value, secret_id: e.target.value })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                  </>
                )}
                {authMethod === 'kubernetes' && (
                  <>
                    <label className="block text-sm">
                      Role
                      <input
                        required
                        className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                        value={field.state.value.role ?? ''}
                        onChange={(e) =>
                          field.handleChange({ ...field.state.value, role: e.target.value })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                    <label className="block text-sm">
                      JWT path (in pod)
                      <input
                        className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                        value={field.state.value.jwt_path ?? ''}
                        onChange={(e) =>
                          field.handleChange({ ...field.state.value, jwt_path: e.target.value })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                  </>
                )}
              </>
            )}
          </form.Field>
          <label className="block text-sm">
            Default mount
            <form.Field name="default_mount">
              {(field) => (
                <input
                  className="block w-full bg-background border border-border rounded p-1.5 mt-1 font-mono"
                  value={field.state.value ?? 'secret'}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                />
              )}
            </form.Field>
          </label>
          <div className="flex gap-2">
            <button
              type="submit"
              disabled={createMu.isPending}
              className="bg-primary text-primary-foreground rounded px-3 py-1.5 text-sm"
            >
              {createMu.isPending ? 'Saving…' : 'Save'}
            </button>
            <button
              type="button"
              onClick={() => {
                setCreating(false);
                setError(null);
              }}
              className="text-sm px-3 py-1.5 border border-border rounded"
            >
              Cancel
            </button>
          </div>
          <p className="text-xs text-muted-foreground">
            Tip: secret fields you don't change in a later edit can be left as
            <code className="ml-1 bg-muted px-1 rounded">{VAULT_AUTH_SENTINEL}</code> to preserve
            the stored value.
          </p>
        </form>
      )}
    </div>
  );
}

function Page() {
  return (
    <SettingsAuthGate>
      <VaultConnectionsPage />
    </SettingsAuthGate>
  );
}

export const Route = createFileRoute('/dashboard/settings/vault/')({
  component: Page,
});
