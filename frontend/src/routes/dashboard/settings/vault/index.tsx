import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { createFileRoute } from "@tanstack/react-router";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/operator-table";
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
import { useState } from "react";
import { KeyRound, Plus, Trash2 } from "lucide-react";
import { ResourceMasthead } from "@/components/ui/page";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";

import { SettingsAuthGate } from "@/components/settings/auth-gate";
import { useAppForm, useStore } from "@/lib/form";
import { FormShell } from "@/components/ui/form-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { QueryStates } from "@/components/ui/query-states";
import { queryKeys } from "@/lib/query-keys";
import { extractApiErrorMessage } from "@/lib/api/errors";
import {
  listVaultConnections,
  createVaultConnection,
  deleteVaultConnection,
  testVaultConnection,
  VAULT_AUTH_SENTINEL,
  type VaultConnectionView,
  type VaultAuthMethod,
  type VaultConnectionWriteRequest,
} from "@/lib/api/vault";

function blankBody(method: VaultAuthMethod): VaultConnectionWriteRequest {
  const auth: Record<string, string> =
    method === "token"
      ? { token: "" }
      : method === "approle"
        ? { role_id: "", secret_id: "" }
        : {
            role: "",
            jwt_path: "/var/run/secrets/kubernetes.io/serviceaccount/token",
          };
  return {
    name: "",
    description: "",
    addr: "https://",
    auth_method: method,
    auth,
    default_mount: "secret",
    namespace: "",
    tls_skip_verify: false,
    ca_cert_pem: "",
    enabled: true,
  };
}

function VaultConnectionsPage() {
  const qc = useQueryClient();
  const vaultConnectionsQuery = useQuery({
    queryKey: queryKeys.vault.connections,
    queryFn: listVaultConnections,
  });
  const { data: rows = [], isLoading } = vaultConnectionsQuery;

  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<VaultConnectionView | null>(
    null,
  );

  const createMu = useMutation({
    mutationFn: (body: VaultConnectionWriteRequest) =>
      createVaultConnection(body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: queryKeys.vault.connections });
    },
    onError: (err: unknown) =>
      setError(extractApiErrorMessage(err) ?? String(err)),
  });

  const form = useAppForm({
    defaultValues: blankBody("token"),
    onSubmit: async ({ value }) => {
      try {
        await createMu.mutateAsync(value);
        setCreating(false);
        form.reset(blankBody("token"));
        setError(null);
      } catch {
        // onError above surfaces the message inline.
      }
    },
  });
  // The auth-blob inputs switch shape on the selected method.
  const authMethod = useStore(form.store, (s) => s.values.auth_method);
  const delMu = useMutation({
    mutationFn: (id: string) => deleteVaultConnection(id),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: queryKeys.vault.connections }),
  });
  const testMu = useMutation({
    mutationFn: (id: string) => testVaultConnection(id),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: queryKeys.vault.connections }),
  });

  if (vaultConnectionsQuery.isError) {
    return (
      <QueryStates
        query={vaultConnectionsQuery}
        permission="vault_connections:list"
      >
        {() => null}
      </QueryStates>
    );
  }

  return (
    <div className="p-6 space-y-6">
      <ResourceMasthead
        backTo="/dashboard/settings"
        backLabel="Back to settings"
        title={
          <span className="inline-flex items-center gap-2">
            <KeyRound className="h-5 w-5" /> Vault connections
          </span>
        }
        actions={
          <button
            onClick={() => setCreating(true)}
            className="inline-flex items-center gap-2 rounded-sm bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            <Plus className="h-4 w-4" /> New connection
          </button>
        }
      />

      <p className="text-sm text-muted-foreground max-w-3xl">
        Vault references in values blobs use the syntax{" "}
        <code className="bg-muted px-1 rounded-sm">
          {"${vault://<connection>/<engine>/<path>#<key>}"}
        </code>
        . References are resolved in-memory at install time; the resolved value
        is never written to the database or audit log.
      </p>

      {isLoading ? (
        <div className="text-muted-foreground">Loading…</div>
      ) : (
        <Table className="w-full text-sm border border-border rounded-sm">
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
              rows.map((row: VaultConnectionView) => (
                <TableRow key={row.id} className="border-t border-border">
                  <TableCell className="p-2 font-medium">{row.name}</TableCell>
                  <TableCell className="p-2 font-mono text-xs">
                    {row.addr}
                  </TableCell>
                  <TableCell className="p-2 uppercase text-xs">
                    {row.authMethod}
                  </TableCell>
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
                      onClick={() => setDeleteTarget(row)}
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
        <FormShell
          className="space-y-3 max-w-xl border border-border rounded-sm p-4"
          onSubmit={(e) => {
            e.preventDefault();
            void form.handleSubmit();
          }}
        >
          <form.AppForm>
            <form.FormErrorSummary serverError={error} />
          </form.AppForm>
          <h2 className="font-medium">New connection</h2>
          <label className="block text-sm">
            Name
            <form.Field name="name">
              {(field) => (
                <Input
                  name={field.name}
                  required
                  className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1"
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
                <Input
                  name={field.name}
                  required
                  className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                  value={field.state.value}
                  onChange={(e) => field.handleChange(e.target.value)}
                  onBlur={field.handleBlur}
                />
              )}
            </form.Field>
          </label>
          <label className="block text-sm">
            Auth method
            <Select
              className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1"
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
            </Select>
          </label>
          <form.Field name="auth">
            {(field) => (
              <>
                {authMethod === "token" && (
                  <label className="block text-sm">
                    Token
                    <Input
                      name={field.name}
                      type="password"
                      required
                      className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                      value={field.state.value.token ?? ""}
                      onChange={(e) =>
                        field.handleChange({ token: e.target.value })
                      }
                      onBlur={field.handleBlur}
                    />
                  </label>
                )}
                {authMethod === "approle" && (
                  <>
                    <label className="block text-sm">
                      Role ID
                      <Input
                        name={field.name}
                        required
                        className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                        value={field.state.value.role_id ?? ""}
                        onChange={(e) =>
                          field.handleChange({
                            ...field.state.value,
                            role_id: e.target.value,
                          })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                    <label className="block text-sm">
                      Secret ID
                      <Input
                        name={field.name}
                        type="password"
                        required
                        className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                        value={field.state.value.secret_id ?? ""}
                        onChange={(e) =>
                          field.handleChange({
                            ...field.state.value,
                            secret_id: e.target.value,
                          })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                  </>
                )}
                {authMethod === "kubernetes" && (
                  <>
                    <label className="block text-sm">
                      Role
                      <Input
                        name={field.name}
                        required
                        className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                        value={field.state.value.role ?? ""}
                        onChange={(e) =>
                          field.handleChange({
                            ...field.state.value,
                            role: e.target.value,
                          })
                        }
                        onBlur={field.handleBlur}
                      />
                    </label>
                    <label className="block text-sm">
                      JWT path (in pod)
                      <Input
                        name={field.name}
                        className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                        value={field.state.value.jwt_path ?? ""}
                        onChange={(e) =>
                          field.handleChange({
                            ...field.state.value,
                            jwt_path: e.target.value,
                          })
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
                <Input
                  name={field.name}
                  className="block w-full bg-background border border-border rounded-sm p-1.5 mt-1 font-mono"
                  value={field.state.value ?? "secret"}
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
              className="bg-primary text-primary-foreground rounded-sm px-3 py-1.5 text-sm"
            >
              {createMu.isPending ? "Saving…" : "Save"}
            </button>
            <button
              type="button"
              onClick={() => {
                setCreating(false);
                setError(null);
              }}
              className="text-sm px-3 py-1.5 border border-border rounded-sm"
            >
              Cancel
            </button>
          </div>
          <p className="text-xs text-muted-foreground">
            Tip: secret fields you don't change in a later edit can be left as
            <code className="ml-1 bg-muted px-1 rounded-sm">
              {VAULT_AUTH_SENTINEL}
            </code>{" "}
            to preserve the stored value.
          </p>
        </FormShell>
      )}
      <ConfirmDialog
        open={deleteTarget !== null}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => {
          if (!deleteTarget) return;
          delMu.mutate(deleteTarget.id, {
            onSuccess: () => setDeleteTarget(null),
          });
        }}
        title="Delete Vault connection"
        description="This permanently removes the encrypted connection configuration."
        confirmValue={deleteTarget?.name}
        variant="destructive"
        loading={delMu.isPending}
        impact={
          deleteTarget
            ? {
                scope: `${deleteTarget.name} (${deleteTarget.addr})`,
                consequences: [
                  "New catalog operations cannot resolve secrets through this connection.",
                  "Stored authentication material cannot be recovered.",
                ],
                recovery: "Create and validate a replacement Vault connection.",
              }
            : undefined
        }
      />
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

export const Route = createFileRoute("/dashboard/settings/vault/")({
  component: Page,
});
