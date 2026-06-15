'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
/**
 * Cluster "Network & access" tab (migration 070).
 *
 * Operator-facing view of the per-cluster apiserver allow-list:
 *   - Mode toggle (monitor / enforce / disabled) with a confirm modal
 *     on the enforce upgrade path. The backend returns 409 if the
 *     mode flip happens while drift exists; the modal surfaces that
 *     409 + offers a "Apply anyway (force)" retry.
 *   - Two CIDR lists side-by-side: "Operator" (editable) and "Astronomer
 *     egress" (read-only with an explainer tooltip).
 *   - Effective list (last reconcile snapshot).
 *   - Drift badge + Reconcile-now button.
 *   - Collapsed snapshot history table.
 *
 * Reads poll every 30s; writes invalidate the query so the next
 * fetch shows the post-write state. The backend's 15m reconciler
 * sweep is the eventual-consistency safety net.
 */

import { useMemo, useState } from 'react';
import { useParams } from 'next/navigation';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { toastError, toastInfo, toastSuccess, toastWarning } from '@/lib/toast';
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Loader2,
  Lock,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
} from 'lucide-react';

import {
  getApiserverAllowlist,
  listApiserverAllowlistSnapshots,
  reconcileApiserverAllowlist,
  updateApiserverAllowlist,
  type ApiserverAllowlistMode,
  type ApiserverAllowlistResponse,
  type ApiserverAllowlistSnapshot,
} from '@/lib/api/cluster-detail';
import { queryKeys } from '@/lib/hooks';
import { usePermissionDecision } from '@/lib/permission-hooks';
import { ConfirmDialog } from '@/components/ui/confirm-dialog';

function useClustersUpdate(clusterId: string): { canWrite: boolean; reason: string } {
  const decision = usePermissionDecision('clusters', 'update', { type: 'cluster', id: clusterId });
  return {
    canWrite: decision.allowed,
    reason: decision.disabledReason ?? '',
  };
}

// ─── Mode badge ─────────────────────────────────────────────────────────────
function ModeBadge({
  mode,
  drift,
}: {
  mode: ApiserverAllowlistMode;
  drift: boolean;
}) {
  if (mode === 'disabled') {
    return (
      <span className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs bg-gray-100 text-gray-700">
        Apiserver: open
      </span>
    );
  }
  if (mode === 'enforce') {
    return (
      <span
        className={
          drift
            ? 'inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs bg-amber-100 text-amber-800'
            : 'inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs bg-green-100 text-green-800'
        }
      >
        <Lock className="h-3 w-3" /> Apiserver: locked
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs bg-blue-100 text-blue-800">
      Apiserver: monitoring
    </span>
  );
}

// ─── CIDR pill ──────────────────────────────────────────────────────────────
function CIDRPill({ cidr, removable, onRemove }: { cidr: string; removable?: boolean; onRemove?: () => void }) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-slate-100 px-2 py-0.5 text-xs font-mono text-slate-700">
      {cidr}
      {removable && onRemove && (
        <button
          type="button"
          onClick={onRemove}
          className="ml-1 text-slate-500 hover:text-red-600"
          aria-label={`remove ${cidr}`}
        >
          ×
        </button>
      )}
    </span>
  );
}

// ─── Main page ──────────────────────────────────────────────────────────────
export default function ClusterNetworkAccessPage() {
  const params = useParams<{ id: string }>();
  const clusterId = params.id;
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const { data, isLoading, isError, refetch } = useQuery({
    queryKey: queryKeys.clusterPages.apiserverAllowlist(clusterId),
    queryFn: () => getApiserverAllowlist(clusterId),
    refetchInterval: 30_000,
  });

  // Editor state — initialised from the server snapshot on first load.
  const [editing, setEditing] = useState<boolean>(false);
  const [editedCIDRs, setEditedCIDRs] = useState<string[]>([]);
  const [editedMode, setEditedMode] = useState<ApiserverAllowlistMode>('monitor');
  const [newCIDR, setNewCIDR] = useState<string>('');
  const [showSnapshots, setShowSnapshots] = useState<boolean>(false);
  const [confirmEnforce, setConfirmEnforce] = useState<boolean>(false);
  const [requireForce, setRequireForce] = useState<boolean>(false);

  // Sync editor state with the latest server payload when not editing.
  useMemo(() => {
    if (data && !editing) {
      setEditedCIDRs(data.operatorCidrs);
      setEditedMode(data.mode);
    }
  }, [data, editing]);

  const updateMut = useMutation({
    mutationFn: (body: { cidrs: string[]; mode: ApiserverAllowlistMode; forceApply?: boolean }) =>
      updateApiserverAllowlist(clusterId, body),
    onSuccess: () => {
      toastSuccess('Apiserver allow-list updated');
      setEditing(false);
      setRequireForce(false);
      queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.apiserverAllowlist(clusterId) });
    },
    onError: (err: any) => {
      const code = err?.response?.data?.error?.code;
      if (code === 'mode_change_requires_force') {
        setRequireForce(true);
        toastWarning(
          'Enforce mode requires force_apply while drift exists — re-submit to apply anyway.',
        );
      } else {
        toastError(
          err?.response?.data?.error?.message ?? 'Failed to update allow-list',
        );
      }
    },
  });

  const reconcileMut = useMutation({
    mutationFn: () => reconcileApiserverAllowlist(clusterId),
    onSuccess: () => {
      toastSuccess('Reconcile queued');
      // The reconciler runs async; refresh after a short delay.
      setTimeout(
        () => queryClient.invalidateQueries({ queryKey: queryKeys.clusterPages.apiserverAllowlist(clusterId) }),
        2_000,
      );
    },
    onError: (err: any) => {
      toastError(
        err?.response?.data?.error?.message ?? 'Failed to queue reconcile',
      );
    },
  });

  const { data: snapshots = [] } = useQuery({
    queryKey: queryKeys.clusterPages.apiserverAllowlistSnapshots(clusterId),
    queryFn: () => listApiserverAllowlistSnapshots(clusterId, { limit: 20 }),
    enabled: showSnapshots,
  });

  function handleSave() {
    if (
      data?.mode === 'monitor' &&
      editedMode === 'enforce' &&
      data?.drift &&
      !requireForce
    ) {
      setConfirmEnforce(true);
      return;
    }
    updateMut.mutate({
      cidrs: editedCIDRs,
      mode: editedMode,
      forceApply: requireForce,
    });
  }

  function handleEnforceConfirm() {
    setConfirmEnforce(false);
    updateMut.mutate({
      cidrs: editedCIDRs,
      mode: 'enforce',
      forceApply: true,
    });
  }

  function handleAddCIDR() {
    const trimmed = newCIDR.trim();
    if (!trimmed) return;
    if (editedCIDRs.includes(trimmed)) {
      toastInfo('CIDR already in list');
      return;
    }
    setEditedCIDRs([...editedCIDRs, trimmed]);
    setNewCIDR('');
  }

  if (isLoading) {
    return (
      <div className="flex items-center gap-2 p-6 text-sm text-slate-500">
        <Loader2 className="h-4 w-4 animate-spin" /> Loading network access…
      </div>
    );
  }

  if (isError || !data) {
    return (
      <div className="p-6 text-sm text-red-700">
        Failed to load network access. <button onClick={() => refetch()}>Retry</button>
      </div>
    );
  }

  return (
    <div className="space-y-6 p-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold flex items-center gap-2">
            Network &amp; access
            <ModeBadge mode={data.mode} drift={data.drift} />
          </h1>
          <p className="text-sm text-slate-500 mt-1">
            Manage the operator-defined CIDR allow-list for this cluster&apos;s apiserver.
            Astronomer&apos;s tunnel egress block is always stamped on top — operators
            can&apos;t remove it without disabling Astronomer management.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {data.drift && (
            <span className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs bg-amber-100 text-amber-800">
              <ShieldAlert className="h-3 w-3" /> Drift detected
            </span>
          )}
          {data.syncStatus === 'synced' && (
            <span className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs bg-green-100 text-green-800">
              <ShieldCheck className="h-3 w-3" /> Synced
            </span>
          )}
          <button
            type="button"
            onClick={() => reconcileMut.mutate()}
            disabled={!canWrite || reconcileMut.isPending}
            title={canWrite ? 'Run reconcile now' : reason}
            className="inline-flex items-center gap-1 rounded border px-3 py-1 text-sm hover:bg-slate-50 disabled:opacity-50"
          >
            <RefreshCw
              className={reconcileMut.isPending ? 'h-4 w-4 animate-spin' : 'h-4 w-4'}
            />
            Reconcile now
          </button>
        </div>
      </div>

      {/* Detected provider + status row */}
      <div className="grid grid-cols-3 gap-4 rounded border bg-slate-50 p-4 text-sm">
        <div>
          <div className="text-slate-500">Detected provider</div>
          <div className="font-mono">{data.detectedProvider}</div>
        </div>
        <div>
          <div className="text-slate-500">Sync status</div>
          <div className="font-mono">{data.syncStatus}</div>
        </div>
        <div>
          <div className="text-slate-500">Last reconciled</div>
          <div className="font-mono">
            {data.lastReconciledAt ?? '—'}
          </div>
        </div>
        {data.lastError && (
          <div className="col-span-3 flex items-start gap-2 rounded bg-amber-50 p-2 text-xs text-amber-800">
            <AlertTriangle className="mt-0.5 h-3 w-3" />
            <span>{data.lastError}</span>
          </div>
        )}
      </div>

      {/* CIDR lists side-by-side */}
      <div className="grid grid-cols-2 gap-4">
        <div className="rounded border p-4">
          <div className="flex items-center justify-between mb-2">
            <h2 className="font-medium">Operator CIDRs</h2>
            {!editing ? (
              <button
                type="button"
                onClick={() => setEditing(true)}
                disabled={!canWrite}
                title={canWrite ? 'Edit' : reason}
                className="text-xs underline disabled:opacity-50"
              >
                Edit
              </button>
            ) : (
              <div className="flex gap-2">
                <button
                  type="button"
                  onClick={() => {
                    setEditing(false);
                    setEditedCIDRs(data.operatorCidrs);
                    setEditedMode(data.mode);
                    setRequireForce(false);
                  }}
                  className="text-xs underline"
                >
                  Cancel
                </button>
                <button
                  type="button"
                  onClick={handleSave}
                  disabled={updateMut.isPending}
                  className="text-xs underline text-blue-700"
                >
                  Save
                </button>
              </div>
            )}
          </div>
          <div className="flex flex-wrap gap-1 min-h-[2rem]">
            {(editing ? editedCIDRs : data.operatorCidrs).map((c) => (
              <CIDRPill
                key={c}
                cidr={c}
                removable={editing}
                onRemove={() => setEditedCIDRs(editedCIDRs.filter((x) => x !== c))}
              />
            ))}
            {(editing ? editedCIDRs : data.operatorCidrs).length === 0 && (
              <span className="text-xs text-slate-400">No operator CIDRs configured.</span>
            )}
          </div>
          {editing && (
            <div className="mt-3 flex gap-2">
              <input
                type="text"
                value={newCIDR}
                onChange={(e) => setNewCIDR(e.target.value)}
                placeholder="e.g. 10.0.0.0/8"
                className="flex-1 rounded border px-2 py-1 text-sm font-mono"
              />
              <button
                type="button"
                onClick={handleAddCIDR}
                className="rounded border px-3 py-1 text-sm hover:bg-slate-50"
              >
                Add
              </button>
            </div>
          )}
        </div>

        <div className="rounded border bg-slate-50 p-4">
          <h2 className="font-medium mb-2 flex items-center gap-1">
            Astronomer egress
            <span
              className="text-xs text-slate-500"
              title="Astronomer's tunnel egress IPs are stamped onto every cluster's allow-list automatically. Operators cannot remove this block — doing so would brick the tunnel."
            >
              ⓘ
            </span>
          </h2>
          <div className="flex flex-wrap gap-1 min-h-[2rem]">
            {data.astronomerEgress.length === 0 ? (
              <span className="text-xs text-slate-400">No egress CIDRs configured.</span>
            ) : (
              data.astronomerEgress.map((c) => <CIDRPill key={c} cidr={c} />)
            )}
          </div>
        </div>
      </div>

      {/* Mode toggle */}
      <div className="rounded border p-4">
        <h2 className="font-medium mb-2">Mode</h2>
        <div className="flex gap-3 text-sm">
          {(['monitor', 'enforce', 'disabled'] as const).map((m) => (
            <label key={m} className="flex items-center gap-1">
              <input
                type="radio"
                name="mode"
                value={m}
                checked={(editing ? editedMode : data.mode) === m}
                onChange={() => setEditedMode(m)}
                disabled={!editing || !canWrite}
              />
              <span className="capitalize">{m}</span>
            </label>
          ))}
        </div>
        <p className="mt-2 text-xs text-slate-500">
          <strong>monitor</strong>: record drift, never patch.{' '}
          <strong>enforce</strong>: patch the cloud LB / firewall on every divergence.{' '}
          <strong>disabled</strong>: no reconciliation.
        </p>
      </div>

      {/* Effective list */}
      <div className="rounded border p-4">
        <h2 className="font-medium mb-2">Effective (last reconcile)</h2>
        <div className="flex flex-wrap gap-1 min-h-[2rem]">
          {data.effective.length === 0 ? (
            <span className="text-xs text-slate-400">
              No effective list captured yet — reconcile to populate.
            </span>
          ) : (
            data.effective.map((c) => <CIDRPill key={c} cidr={c} />)
          )}
        </div>
      </div>

      {/* Desired (preview) */}
      <div className="rounded border p-4">
        <h2 className="font-medium mb-2 flex items-center gap-1">
          Desired
          <CheckCircle2 className="h-3 w-3 text-green-600" />
        </h2>
        <div className="flex flex-wrap gap-1 min-h-[2rem]">
          {data.desired.map((c) => (
            <CIDRPill key={c} cidr={c} />
          ))}
        </div>
      </div>

      {/* Snapshot history */}
      <div className="rounded border">
        <button
          type="button"
          onClick={() => setShowSnapshots(!showSnapshots)}
          className="flex w-full items-center justify-between p-3 text-sm font-medium hover:bg-slate-50"
        >
          <span>Snapshot history</span>
          {showSnapshots ? (
            <ChevronDown className="h-4 w-4" />
          ) : (
            <ChevronRight className="h-4 w-4" />
          )}
        </button>
        {showSnapshots && (
          <div className="border-t p-3">
            {snapshots.length === 0 ? (
              <p className="text-sm text-slate-400">No snapshots captured yet.</p>
            ) : (
              <Table className="w-full text-sm">
                <TableHeader>
                  <TableRow className="text-left text-xs text-slate-500">
                    <TableHead className="py-1">Captured</TableHead>
                    <TableHead>Drift</TableHead>
                    <TableHead>Effective</TableHead>
                    <TableHead>Desired</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {snapshots.map((s) => (
                    <TableRow key={s.id} className="border-t text-xs">
                      <TableCell className="py-1 font-mono">{s.capturedAt}</TableCell>
                      <TableCell>{s.drift ? '⚠ yes' : 'no'}</TableCell>
                      <TableCell className="font-mono">{s.effectiveCidrs.length}</TableCell>
                      <TableCell className="font-mono">{s.desiredCidrs.length}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        )}
      </div>

      {/* Enforce confirm modal */}
      <ConfirmDialog
        open={confirmEnforce}
        title="Switch to enforce mode?"
        description={
          'Switching to enforce will patch the cloud LB / firewall on the next reconcile. ' +
          "If drift exists this can lock out a CIDR that's currently allowed but not in your operator list."
        }
        confirmText="Apply anyway (force)"
        onConfirm={handleEnforceConfirm}
        onClose={() => setConfirmEnforce(false)}
      />
    </div>
  );
}
