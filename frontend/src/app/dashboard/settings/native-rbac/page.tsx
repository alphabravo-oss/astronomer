'use client';

/**
 * /dashboard/settings/native-rbac — Native per-CRD RBAC rules.
 *
 * Native rules are an ADDITIVE allow layer: each rule GRANTS access on an exact
 * (apiGroup, resource, verb) tuple even when the coarse `custom_resources`
 * permission doesn't, letting operators scope access per-CRD (e.g. "read
 * cert-manager Certificates but not other CRDs"). They never widen
 * privilege-escalation api groups and never grant exec/logs — the backend
 * rejects those with a 400.
 *
 * The feature is gated server-side behind `native_rbac_enabled`; when off the
 * API 404s and the table degrades to its error state instead of crashing.
 */
import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@/lib/link';
import { ArrowLeft, KeyRound, Loader2, Plus, Trash2 } from 'lucide-react';
import { DataTable, type Column } from '@/components/ui/data-table';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';
import { ModalShell } from '@/components/ui/modal-shell';
import { SettingsAuthGate } from '@/components/settings/auth-gate';
import { queryKeys, useClusters, useUsers } from '@/lib/hooks';
import { toastApiError, toastSuccess } from '@/lib/toast';
import { formatRelativeTime } from '@/lib/utils';
import {
  createNativeRule,
  deleteNativeRule,
  listNativeRules,
  type NativeRule,
  type NativeRuleVerb,
} from '@/lib/api/native-rbac';

// Verb vocabulary rendered as checkboxes. `*` is a distinct "all" option; the
// backend rejects exec/logs so they are intentionally absent.
const VERB_OPTIONS: NativeRuleVerb[] = [
  'read',
  'list',
  'watch',
  'create',
  'update',
  'delete',
  '*',
];

function NativeRbacList() {
  const queryClient = useQueryClient();
  const rulesQuery = useQuery({
    queryKey: queryKeys.nativeRbac.list(),
    queryFn: () => listNativeRules(),
  });
  const { data: usersPage } = useUsers({ pageSize: 200 });

  const userLabel = useMemo(() => {
    const map = new Map<string, string>();
    for (const u of usersPage?.data ?? []) {
      map.set(u.id, u.displayName || u.email || u.username);
    }
    return map;
  }, [usersPage]);

  const [showCreate, setShowCreate] = useState(false);
  const [confirmDelete, setConfirmDelete] = useState<NativeRule | null>(null);

  const del = useMutation({
    mutationFn: (id: string) => deleteNativeRule(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.nativeRbac.all });
      toastSuccess('Rule deleted');
      setConfirmDelete(null);
    },
    onError: (error: Error) => toastApiError('Failed to delete rule', error),
  });

  const columns: Column<NativeRule>[] = [
    {
      key: 'user',
      header: 'User',
      accessor: (row) => (
        <span className="text-sm text-foreground">
          {userLabel.get(row.userId) ?? (
            <span className="font-mono text-xs text-muted-foreground">{row.userId}</span>
          )}
        </span>
      ),
    },
    {
      key: 'cluster',
      header: 'Cluster',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.clusterId ? row.clusterId : 'All'}
        </span>
      ),
    },
    {
      key: 'namespace',
      header: 'Namespace',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.namespace ? row.namespace : 'All'}
        </span>
      ),
    },
    {
      key: 'apiGroup',
      header: 'API Group',
      accessor: (row) => (
        <span className="text-xs font-mono text-muted-foreground">
          {row.apiGroup ? row.apiGroup : 'core'}
        </span>
      ),
    },
    {
      key: 'resource',
      header: 'Resource',
      accessor: (row) => (
        <span className="text-sm font-mono text-foreground">{row.resource}</span>
      ),
    },
    {
      key: 'verbs',
      header: 'Verbs',
      sortable: false,
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {row.verbs.map((v) => (
            <span
              key={v}
              className="px-1.5 py-0.5 rounded bg-muted text-2xs font-mono text-muted-foreground"
            >
              {v}
            </span>
          ))}
        </div>
      ),
    },
    {
      key: 'createdAt',
      header: 'Created',
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.createdAt)}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      sortable: false,
      accessor: (row) => (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            setConfirmDelete(row);
          }}
          className="p-1.5 rounded text-muted-foreground hover:text-status-error hover:bg-status-error/10 transition-colors"
          title="Delete rule"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      ),
    },
  ];

  return (
    <>
      <div className="flex items-center justify-end">
        <button
          type="button"
          onClick={() => setShowCreate(true)}
          className="inline-flex items-center gap-2 h-9 px-4 rounded-lg bg-primary text-primary-foreground text-sm font-medium hover:opacity-90 transition-opacity"
        >
          <Plus className="h-4 w-4" />
          New rule
        </button>
      </div>

      <DataTable
        data={rulesQuery.data ?? []}
        columns={columns}
        keyExtractor={(row) => row.id}
        loading={rulesQuery.isLoading}
        isError={rulesQuery.isError}
        errorMessage="Could not load native RBAC rules. The feature may be disabled server-side (native_rbac_enabled)."
        onRetry={() => rulesQuery.refetch()}
        emptyMessage="No native RBAC rules defined"
        searchPlaceholder="Search rules..."
      />

      {showCreate && <NewRuleModal onClose={() => setShowCreate(false)} />}

      <ConfirmDialog
        open={!!confirmDelete}
        onClose={() => setConfirmDelete(null)}
        onConfirm={() => confirmDelete && del.mutate(confirmDelete.id)}
        title="Delete native RBAC rule?"
        description={`This revokes the granted access on ${confirmDelete?.resource ?? ''}. It does not affect any other rule or the user's coarse permissions.`}
        confirmText="Delete"
        variant="destructive"
        loading={del.isPending}
      />
    </>
  );
}

function NewRuleModal({ onClose }: { onClose: () => void }) {
  const queryClient = useQueryClient();
  const { data: usersPage } = useUsers({ pageSize: 200 });
  const { data: clustersPage } = useClusters({ pageSize: 100 });
  const users = usersPage?.data ?? [];
  const clusters = clustersPage?.data ?? [];

  const [form, setForm] = useState({
    userId: '',
    clusterId: '',
    namespace: '',
    apiGroup: '',
    resource: '',
  });
  const [verbs, setVerbs] = useState<Set<NativeRuleVerb>>(new Set());

  const toggleVerb = (v: NativeRuleVerb) => {
    setVerbs((prev) => {
      const next = new Set(prev);
      if (next.has(v)) next.delete(v);
      else next.add(v);
      return next;
    });
  };

  const create = useMutation({
    mutationFn: () =>
      createNativeRule({
        userId: form.userId,
        clusterId: form.clusterId || undefined,
        namespace: form.namespace.trim() || undefined,
        apiGroup: form.apiGroup.trim() || undefined,
        resource: form.resource.trim(),
        verbs: Array.from(verbs),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: queryKeys.nativeRbac.all });
      toastSuccess('Native RBAC rule created');
      onClose();
    },
    onError: (error: Error) => toastApiError('Failed to create rule', error),
  });

  const canSubmit = form.userId && form.resource.trim() && verbs.size > 0;

  return (
    <ModalShell
      title="New native RBAC rule"
      onClose={onClose}
      panelClassName="max-w-lg bg-popover overflow-hidden"
      footerClassName="bg-muted/30"
      titleIcon={
        <div className="w-8 h-8 rounded-lg bg-muted flex items-center justify-center">
          <KeyRound className="h-4 w-4 text-muted-foreground" />
        </div>
      }
      footer={
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onClose}
            disabled={create.isPending}
            className="inline-flex items-center h-8 px-3 rounded text-sm
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            Cancel
          </button>
          <button
            onClick={() => create.mutate()}
            disabled={!canSubmit || create.isPending}
            className="inline-flex items-center gap-1.5 h-8 px-4 rounded text-sm font-medium
              bg-primary text-primary-foreground hover:bg-primary/90 transition-colors
              disabled:opacity-50 disabled:cursor-not-allowed"
          >
            {create.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Create rule
          </button>
        </div>
      }
    >
      <p className="text-xs text-muted-foreground">
        Native rules are an additive allow layer: they GRANT access on an exact
        (apiGroup, resource, verb) even when the coarse{' '}
        <span className="font-mono">custom_resources</span> permission doesn&apos;t.
        They never widen escalation groups and never grant exec/logs.
      </p>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">User</label>
        <select
          value={form.userId}
          onChange={(e) => setForm({ ...form, userId: e.target.value })}
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
            focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">Select a user…</option>
          {users.map((u) => (
            <option key={u.id} value={u.id}>
              {u.displayName || u.email || u.username}
            </option>
          ))}
        </select>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Cluster</label>
        <select
          value={form.clusterId}
          onChange={(e) => setForm({ ...form, clusterId: e.target.value })}
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm
            focus:outline-none focus:ring-1 focus:ring-ring"
        >
          <option value="">All clusters</option>
          {clusters.map((c) => (
            <option key={c.id} value={c.id}>
              {c.displayName} ({c.name})
            </option>
          ))}
        </select>
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">Namespace</label>
          <input
            type="text"
            value={form.namespace}
            onChange={(e) => setForm({ ...form, namespace: e.target.value })}
            placeholder="All namespaces"
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
        <div className="space-y-1.5">
          <label className="text-sm font-medium text-foreground">API Group</label>
          <input
            type="text"
            value={form.apiGroup}
            onChange={(e) => setForm({ ...form, apiGroup: e.target.value })}
            placeholder="core (empty)"
            className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
              focus:outline-none focus:ring-1 focus:ring-ring"
          />
        </div>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Resource</label>
        <input
          type="text"
          value={form.resource}
          onChange={(e) => setForm({ ...form, resource: e.target.value })}
          placeholder="certificates (or *)"
          className="w-full h-9 px-3 rounded-md border border-border bg-background text-sm font-mono
            focus:outline-none focus:ring-1 focus:ring-ring"
        />
        <p className="text-xs text-muted-foreground">
          Plural resource name (e.g. <span className="font-mono">certificates</span>)
          or <span className="font-mono">*</span> for all resources in the group.
        </p>
      </div>

      <div className="space-y-1.5">
        <label className="text-sm font-medium text-foreground">Verbs</label>
        <div className="flex flex-wrap gap-3">
          {VERB_OPTIONS.map((v) => (
            <label key={v} className="flex items-center gap-1.5 text-sm">
              <input
                type="checkbox"
                checked={verbs.has(v)}
                onChange={() => toggleVerb(v)}
                className="h-4 w-4 rounded border-border"
              />
              <span className="text-foreground font-mono">{v === '*' ? '* (all)' : v}</span>
            </label>
          ))}
        </div>
      </div>
    </ModalShell>
  );
}

export default function NativeRbacPage() {
  return (
    <SettingsAuthGate>
      <div className="space-y-6">
        <Link
          href="/dashboard/settings"
          className="inline-flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors"
        >
          <ArrowLeft className="h-3.5 w-3.5" />
          Back to Settings
        </Link>
        <div>
          <p className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
            Settings · Native RBAC
          </p>
          <h1 className="text-2xl font-semibold text-foreground tracking-tight mt-1">
            Native per-CRD RBAC rules
          </h1>
          <p className="text-sm text-muted-foreground mt-1">
            An additive allow layer that grants access on an exact (apiGroup,
            resource, verb) — scope a user to a single CRD without widening the
            coarse <span className="font-mono">custom_resources</span> permission.
            Escalation groups and exec/logs are never granted.
          </p>
        </div>
        <NativeRbacList />
      </div>
    </SettingsAuthGate>
  );
}
