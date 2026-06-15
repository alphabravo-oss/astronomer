'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Cluster Registries tab — private image-pull credentials, per cluster.
 *
 * Each registry materialises as a docker-registry Secret in the namespaces
 * the user picks (or every project namespace if the list is empty). The
 * "test" action exercises the registry through the agent tunnel so we
 * surface auth failures before they reach a Pod.
 */

import { useMemo, useState } from 'react';
import { useParams } from '@/lib/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastApiError, toastError, toastSuccess } from '@/lib/toast';
import {
  CheckCircle2,
  Container,
  Eye,
  EyeOff,
  Loader2,
  Lock,
  Pencil,
  Plug,
  Plus,
  Server,
  Trash2,
  XCircle,
} from 'lucide-react';

import { queryKeys, useCluster, useClusterNamespaces } from '@/lib/hooks';
import { usePermissionDecision } from '@/lib/permission-hooks';
import {
  createClusterRegistry,
  deleteClusterRegistry,
  listClusterRegistries,
  testClusterRegistry,
  updateClusterRegistry,
  type ClusterRegistry,
  type CreateRegistryRequest,
  type UpdateRegistryRequest,
} from '@/lib/api/cluster-detail';
import { cn } from '@/lib/utils';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { OverlayShell } from '@/components/ui/overlay-shell';

const PASSWORD_SENTINEL = '<set>';

function useClustersUpdate(clusterId: string): { canWrite: boolean; reason: string } {
  const decision = usePermissionDecision('clusters', 'update', { type: 'cluster', id: clusterId });
  return { canWrite: decision.allowed, reason: decision.disabledReason ?? '' };
}

function fmt(iso?: string) {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export default function ClusterRegistriesPage() {
  const params = useParams();
  const clusterId = params.id as string;
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data: cluster, isLoading: clusterLoading } = useCluster(clusterId);
  const { data: registries, isLoading } = useQuery({
    queryKey: queryKeys.clusterPages.registries(clusterId),
    queryFn: () => listClusterRegistries(clusterId),
    enabled: !!clusterId,
    refetchInterval: 30000,
    refetchIntervalInBackground: false,
  });

  const [newOpen, setNewOpen] = useState(false);
  const [editTarget, setEditTarget] = useState<ClusterRegistry | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<ClusterRegistry | null>(null);
  const [testStatus, setTestStatus] = useState<Record<string, 'ok' | 'fail' | 'pending'>>({});

  const deleteMutation = useMutation({
    mutationFn: (registryId: string) => deleteClusterRegistry(clusterId, registryId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.registries(clusterId) });
      toastSuccess('Registry removed');
      setDeleteTarget(null);
    },
    onError: (e: Error) => toastApiError('Delete failed', e),
  });

  const testMutation = useMutation({
    mutationFn: (registryId: string) => testClusterRegistry(clusterId, registryId),
    onMutate: (registryId: string) => {
      setTestStatus((s) => ({ ...s, [registryId]: 'pending' }));
    },
    onSuccess: (res, registryId) => {
      setTestStatus((s) => ({ ...s, [registryId]: res.ok ? 'ok' : 'fail' }));
      if (res.ok) {
        toastSuccess(`Registry reachable${res.latencyMs ? ` (${res.latencyMs}ms)` : ''}`);
      } else {
        toastError(res.message || 'Registry test failed');
      }
    },
    onError: (e: Error, registryId) => {
      setTestStatus((s) => ({ ...s, [registryId]: 'fail' }));
      toastApiError('Test failed', e);
    },
  });

  if (clusterLoading) {
    return (
      <div className="flex items-center justify-center h-64">
        <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
      </div>
    );
  }
  if (!cluster) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Cluster not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight">Registries</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Private image-pull credentials reconciled into namespaces on {cluster.displayName}.
          </p>
        </div>
        <button
          onClick={() => canWrite && setNewOpen(true)}
          disabled={!canWrite}
          title={canWrite ? undefined : reason}
          className="inline-flex items-center gap-1.5 h-9 px-3 rounded-lg text-sm font-medium
            bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Plus className="h-3.5 w-3.5" />
          New Registry
        </button>
      </div>

      {isLoading ? (
        <div className="rounded-lg border border-border bg-card p-12 flex items-center justify-center">
          <Loader2 className="h-5 w-5 animate-spin text-muted-foreground" />
        </div>
      ) : !registries || registries.length === 0 ? (
        <div className="rounded-lg border border-border bg-card p-12 flex flex-col items-center justify-center text-muted-foreground">
          <Container className="h-10 w-10 mb-3" />
          <p className="text-sm font-medium text-foreground">No private registries configured</p>
          <p className="text-xs mt-1 max-w-md text-center">
            Add a registry to mount image-pull secrets into namespaces on this cluster.
          </p>
          <button
            onClick={() => canWrite && setNewOpen(true)}
            disabled={!canWrite}
            title={canWrite ? undefined : reason}
            className="mt-4 inline-flex items-center gap-1.5 h-8 px-3 rounded text-xs font-medium
              border border-border text-foreground hover:bg-accent transition-colors
              disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <Plus className="h-3.5 w-3.5" /> Add registry
          </button>
        </div>
      ) : (
        <div className="rounded-lg border border-border overflow-hidden">
          <Table className="w-full text-sm">
            <TableHeader className="bg-muted/30 text-xs text-muted-foreground">
              <TableRow>
                <TableHead className="text-left font-medium px-4 py-2.5">Registry</TableHead>
                <TableHead className="text-left font-medium px-4 py-2.5">User</TableHead>
                <TableHead className="text-left font-medium px-4 py-2.5">Namespaces</TableHead>
                <TableHead className="text-left font-medium px-4 py-2.5">Default SA</TableHead>
                <TableHead className="text-left font-medium px-4 py-2.5">Last applied</TableHead>
                <TableHead className="text-right font-medium px-4 py-2.5">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody className="divide-y divide-border">
              {registries.map((r) => (
                <TableRow key={r.id} className="hover:bg-accent/30 align-top">
                  <TableCell className="px-4 py-2.5">
                    <div className="font-mono text-xs text-foreground break-all">{r.registryUrl}</div>
                    {r.lastApplyError ? (
                      <div className="text-xs text-status-error mt-1">{r.lastApplyError}</div>
                    ) : null}
                  </TableCell>
                  <TableCell className="px-4 py-2.5 text-xs text-muted-foreground font-mono">{r.username}</TableCell>
                  <TableCell className="px-4 py-2.5">
                    <div className="flex flex-wrap gap-1">
                      {r.namespaces.length === 0 ? (
                        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-xs bg-muted text-muted-foreground border border-border">
                          (all project namespaces)
                        </span>
                      ) : (
                        r.namespaces.map((ns) => (
                          <span
                            key={ns}
                            className="inline-flex items-center px-1.5 py-0.5 rounded text-xs bg-muted text-muted-foreground border border-border"
                          >
                            {ns}
                          </span>
                        ))
                      )}
                    </div>
                  </TableCell>
                  <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
                    {r.injectDefaultSa ? 'Yes' : 'No'}
                  </TableCell>
                  <TableCell className="px-4 py-2.5 text-xs text-muted-foreground">
                    <div className="flex items-center gap-2">
                      <span>{fmt(r.lastAppliedAt)}</span>
                      <TestStatusPill state={testStatus[r.id]} />
                    </div>
                  </TableCell>
                  <TableCell className="px-4 py-2.5">
                    <div className="flex items-center justify-end gap-1.5">
                      <button
                        onClick={() => testMutation.mutate(r.id)}
                        disabled={testMutation.isPending && testStatus[r.id] === 'pending'}
                        title="Test reachability"
                        className="inline-flex items-center gap-1 h-7 px-2 rounded text-xs text-muted-foreground
                          hover:text-foreground hover:bg-accent transition-colors disabled:opacity-50"
                      >
                        <Plug className="h-3.5 w-3.5" />
                        Test
                      </button>
                      <button
                        onClick={() => canWrite && setEditTarget(r)}
                        disabled={!canWrite}
                        title={canWrite ? 'Edit' : reason}
                        className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                          hover:text-foreground hover:bg-accent transition-colors
                          disabled:opacity-50 disabled:cursor-not-allowed"
                      >
                        <Pencil className="h-3.5 w-3.5" />
                      </button>
                      <button
                        onClick={() => canWrite && setDeleteTarget(r)}
                        disabled={!canWrite}
                        title={canWrite ? 'Delete' : reason}
                        className="inline-flex items-center justify-center h-7 w-7 rounded text-muted-foreground
                          hover:text-status-error hover:bg-status-error/10 transition-colors
                          disabled:opacity-50 disabled:cursor-not-allowed"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}

      {newOpen && (
        <RegistryDialog
          clusterId={clusterId}
          onClose={() => setNewOpen(false)}
        />
      )}
      {editTarget && (
        <RegistryDialog
          clusterId={clusterId}
          existing={editTarget}
          onClose={() => setEditTarget(null)}
        />
      )}

      <ConfirmDialog
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        onConfirm={() => deleteTarget && deleteMutation.mutate(deleteTarget.id)}
        title="Remove registry"
        description={
          deleteTarget
            ? `Delete the registry binding for "${deleteTarget.registryUrl}"? The associated docker-registry Secrets will also be removed from the cluster.`
            : ''
        }
        confirmText="Delete"
        variant="destructive"
        loading={deleteMutation.isPending}
      />
    </div>
  );
}

function TestStatusPill({ state }: { state?: 'ok' | 'fail' | 'pending' }) {
  if (!state) return null;
  if (state === 'pending') {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] border bg-status-info/10 text-status-info border-status-info/20">
        <Loader2 className="h-3 w-3 animate-spin" /> Testing
      </span>
    );
  }
  if (state === 'ok') {
    return (
      <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] border bg-status-success/10 text-status-success border-status-success/20">
        <CheckCircle2 className="h-3 w-3" /> Reachable
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] border bg-status-error/10 text-status-error border-status-error/20">
      <XCircle className="h-3 w-3" /> Failed
    </span>
  );
}

// ─── Registry create/edit dialog ────────────────────────────────────────────
function RegistryDialog({
  clusterId,
  existing,
  onClose,
}: {
  clusterId: string;
  existing?: ClusterRegistry;
  onClose: () => void;
}) {
  const queryClient = useQueryClient();
  const isEdit = !!existing;
  const { data: namespaces } = useClusterNamespaces(clusterId);

  const [registryUrl, setRegistryUrl] = useState(existing?.registryUrl || '');
  const [username, setUsername] = useState(existing?.username || '');
  const [password, setPassword] = useState(isEdit ? PASSWORD_SENTINEL : '');
  const [passwordTouched, setPasswordTouched] = useState(false);
  const [showPassword, setShowPassword] = useState(false);
  const [selectedNs, setSelectedNs] = useState<string[]>(existing?.namespaces || []);
  const [secretName, setSecretName] = useState(existing?.secretName || '');
  const [injectDefaultSa, setInjectDefaultSa] = useState(existing?.injectDefaultSa ?? false);

  const create = useMutation({
    mutationFn: (body: CreateRegistryRequest) => createClusterRegistry(clusterId, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.registries(clusterId) });
      toastSuccess('Registry added');
      onClose();
    },
    onError: (e: Error) => toastApiError('Create failed', e),
  });
  const update = useMutation({
    mutationFn: (body: UpdateRegistryRequest) =>
      updateClusterRegistry(clusterId, existing!.id, body),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.registries(clusterId) });
      toastSuccess('Registry updated');
      onClose();
    },
    onError: (e: Error) => toastApiError('Update failed', e),
  });

  const loading = create.isPending || update.isPending;

  function handleSubmit() {
    if (!registryUrl || !username) {
      toastError('Registry URL and username are required');
      return;
    }
    if (isEdit) {
      const body: UpdateRegistryRequest = {
        registry_url: registryUrl,
        username,
        namespaces: selectedNs,
        secret_name: secretName || undefined,
        inject_default_sa: injectDefaultSa,
      };
      if (passwordTouched && password !== PASSWORD_SENTINEL) {
        body.password = password;
      }
      update.mutate(body);
    } else {
      if (!password) {
        toastError('Password is required');
        return;
      }
      create.mutate({
        registry_url: registryUrl,
        username,
        password,
        namespaces: selectedNs,
        secret_name: secretName || undefined,
        inject_default_sa: injectDefaultSa,
      });
    }
  }

  return (
    <Modal
      title={isEdit ? `Edit ${existing.registryUrl}` : 'Add registry'}
      icon={<Lock className="h-4 w-4" />}
      onClose={onClose}
    >
      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Registry URL</label>
        <input
          type="text"
          value={registryUrl}
          onChange={(e) => setRegistryUrl(e.target.value)}
          placeholder="e.g. registry.example.com or 123.dkr.ecr.us-east-1.amazonaws.com"
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm font-mono
            placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Username</label>
          <input
            type="text"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-2 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Password</label>
          <div className="relative">
            <input
              type={showPassword ? 'text' : 'password'}
              value={password}
              onChange={(e) => {
                setPassword(e.target.value);
                setPasswordTouched(true);
              }}
              onFocus={() => {
                if (isEdit && !passwordTouched && password === PASSWORD_SENTINEL) {
                  setPassword('');
                  setPasswordTouched(true);
                }
              }}
              className="w-full h-9 pl-3 pr-9 rounded-lg border border-border bg-background text-sm font-mono
                focus:outline-none focus:ring-2 focus:ring-ring"
            />
            <button
              type="button"
              onClick={() => setShowPassword((v) => !v)}
              className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
              aria-label={showPassword ? 'Hide password' : 'Show password'}
            >
              {showPassword ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
            </button>
          </div>
          {isEdit && !passwordTouched && (
            <p className="text-xs text-muted-foreground">
              Leave untouched to keep the existing password.
            </p>
          )}
        </div>
      </div>

      <NamespaceMultiSelect
        namespaces={namespaces?.map((n) => n.name) || []}
        selected={selectedNs}
        onChange={setSelectedNs}
      />

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Secret name</label>
        <input
          type="text"
          value={secretName}
          onChange={(e) => setSecretName(e.target.value)}
          placeholder="auto"
          className="w-full h-9 px-3 rounded-lg border border-border bg-background text-sm font-mono
            placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring"
        />
        <p className="text-xs text-muted-foreground">
          Leave blank to auto-generate a secret name from the registry URL.
        </p>
      </div>

      <label className="flex items-center gap-2 text-sm text-foreground cursor-pointer select-none">
        <input
          type="checkbox"
          checked={injectDefaultSa}
          onChange={(e) => setInjectDefaultSa(e.target.checked)}
          className="h-4 w-4"
        />
        Attach to <code className="font-mono text-xs">default</code> ServiceAccount in each namespace
      </label>

      <ModalFooter
        onCancel={onClose}
        onSubmit={handleSubmit}
        loading={loading}
        submitLabel={isEdit ? 'Save' : 'Add registry'}
      />
    </Modal>
  );
}

// ─── Reused multi-select (kept local for now — small enough not to share) ───
function NamespaceMultiSelect({
  namespaces,
  selected,
  onChange,
}: {
  namespaces: string[];
  selected: string[];
  onChange: (ns: string[]) => void;
}) {
  const sorted = useMemo(() => [...namespaces].sort(), [namespaces]);
  const [filter, setFilter] = useState('');
  const filtered = sorted.filter((n) => n.toLowerCase().includes(filter.toLowerCase()));
  const toggle = (n: string) =>
    onChange(selected.includes(n) ? selected.filter((x) => x !== n) : [...selected, n]);

  return (
    <div className="space-y-1.5">
      <label className="text-sm font-medium text-foreground">
        Namespaces{' '}
        <span className="text-xs text-muted-foreground font-normal">
          (empty = all project namespaces)
        </span>
      </label>
      <input
        type="text"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        placeholder="Filter namespaces…"
        className="w-full h-8 px-2.5 rounded-md border border-border bg-background text-xs
          placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
      />
      <div className="rounded-md border border-border bg-background max-h-40 overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="text-xs text-muted-foreground px-3 py-2">No namespaces match.</div>
        ) : (
          filtered.map((ns) => (
            <label key={ns} className="flex items-center gap-2 px-3 py-1 text-xs hover:bg-accent/40 cursor-pointer">
              <input
                type="checkbox"
                checked={selected.includes(ns)}
                onChange={() => toggle(ns)}
                className="h-3.5 w-3.5"
              />
              <span className="font-mono">{ns}</span>
            </label>
          ))
        )}
      </div>
      {selected.length > 0 && (
        <div className="flex flex-wrap gap-1 pt-1">
          {selected.map((ns) => (
            <span
              key={ns}
              className={cn(
                'inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-xs border',
                'bg-muted border-border text-muted-foreground',
              )}
            >
              {ns}
              <button onClick={() => toggle(ns)} className="hover:text-foreground" aria-label={`Remove ${ns}`}>
                <XCircle className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      )}
    </div>
  );
}

function Modal({
  title,
  icon,
  onClose,
  children,
}: {
  title: string;
  icon?: React.ReactNode;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <OverlayShell onClose={onClose}>
      <div className="relative w-full max-w-lg max-h-[90vh] flex flex-col rounded-xl border border-border bg-popover shadow-2xl overflow-hidden">
        <div className="flex items-center justify-between px-6 py-4 border-b border-border flex-shrink-0">
          <div className="flex items-center gap-3 min-w-0">
            {icon && (
              <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center text-muted-foreground flex-shrink-0">
                {icon}
              </div>
            )}
            <h3 className="text-lg font-semibold text-foreground truncate">{title}</h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground transition-colors flex-shrink-0"
            aria-label="Close"
          >
            <XCircle className="h-5 w-5" />
          </button>
        </div>
        <div className="p-6 space-y-4 overflow-y-auto">{children}</div>
      </div>
    </OverlayShell>
  );
}

function ModalFooter({
  onCancel,
  onSubmit,
  loading,
  submitLabel,
}: {
  onCancel: () => void;
  onSubmit: () => void;
  loading?: boolean;
  submitLabel: string;
}) {
  return (
    <div className="flex items-center justify-end gap-2 pt-3 -mx-6 px-6 border-t border-border">
      <div className="pt-3 flex items-center gap-2">
        <button
          onClick={onCancel}
          disabled={loading}
          className="inline-flex items-center h-9 px-3 rounded text-sm
            text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
        >
          Cancel
        </button>
        <button
          onClick={onSubmit}
          disabled={loading}
          className="inline-flex items-center gap-2 h-9 px-4 rounded text-sm font-medium
            bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
            disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
          {submitLabel}
        </button>
      </div>
    </div>
  );
}
