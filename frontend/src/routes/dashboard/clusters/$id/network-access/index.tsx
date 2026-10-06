import { Input } from "@/components/ui/input";
import { createFileRoute } from "@tanstack/react-router";
import { DataTable, type Column } from "@/components/ui/data-table";
import { PageHeader, PageShell } from "@/components/ui/page";
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

import { useState } from "react";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { QueryStates } from "@/components/ui/query-states";
import { toastError, toastInfo, toastSuccess, toastWarning } from "@/lib/toast";
import { extractApiErrorMessage } from "@/lib/api/errors";
import type { AxiosError } from "axios";
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Lock,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
} from "lucide-react";

import {
  getApiserverAllowlist,
  listApiserverAllowlistSnapshots,
  reconcileApiserverAllowlist,
  updateApiserverAllowlist,
  type ApiserverAllowlistMode,
  type ApiserverAllowlistSnapshot,
} from "@/lib/api/cluster-apiserver-allowlist";
import { queryKeys } from "@/lib/query-keys";
import { liveFallback } from "@/lib/live/status-store";
import { useClustersUpdate } from "@/lib/permission-hooks";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { ActionButton } from "@/components/ui/action-button";
import { Tooltip } from "@/components/ui/tooltip";
import { BARE_BUTTON } from "@/lib/bare-button";

// ─── Mode badge ─────────────────────────────────────────────────────────────
function ModeBadge({
  mode,
  drift,
}: {
  mode: ApiserverAllowlistMode;
  drift: boolean;
}) {
  if (mode === "disabled") {
    return (
      <span className="inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs bg-muted text-muted-foreground">
        Apiserver: open
      </span>
    );
  }
  if (mode === "enforce") {
    return (
      <span
        className={
          drift
            ? "inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs bg-status-warning/10 text-status-warning"
            : "inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs bg-status-success/10 text-status-success"
        }
      >
        <Lock className="h-3 w-3" /> Apiserver: locked
      </span>
    );
  }
  return (
    <span className="inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs bg-status-info/10 text-status-info">
      Apiserver: monitoring
    </span>
  );
}

// ─── CIDR pill ──────────────────────────────────────────────────────────────
function CIDRPill({
  cidr,
  removable,
  onRemove,
}: {
  cidr: string;
  removable?: boolean;
  onRemove?: () => void;
}) {
  return (
    <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs font-mono text-foreground">
      {cidr}
      {removable && onRemove && (
        <ActionButton
          {...BARE_BUTTON}
          onClick={onRemove}
          className="ml-1 text-muted-foreground hover:text-status-error inline-block font-normal"
          aria-label={`remove ${cidr}`}
        >
          ×
        </ActionButton>
      )}
    </span>
  );
}

// ─── Snapshot history table ─────────────────────────────────────────────────
const snapshotColumns: Column<ApiserverAllowlistSnapshot>[] = [
  {
    key: "capturedAt",
    header: "Captured",
    kind: "name",
    minSize: 220,
    accessor: (s) => <span className="font-mono text-xs">{s.capturedAt}</span>,
    sortAccessor: (s) => s.capturedAt,
  },
  {
    key: "drift",
    header: "Drift",
    kind: "status",
    size: 96,
    accessor: (s) => (
      <span className="text-xs">{s.drift ? "⚠ yes" : "no"}</span>
    ),
    searchAccessor: (s) => (s.drift ? "yes" : "no"),
    sortAccessor: (s) => (s.drift ? 1 : 0),
    filter: { label: "Drift" },
  },
  {
    key: "effective",
    header: "Effective",
    kind: "count",
    accessor: (s) => (
      <span className="font-mono text-xs">{s.effectiveCidrs.length}</span>
    ),
    sortAccessor: (s) => s.effectiveCidrs.length,
  },
  {
    key: "desired",
    header: "Desired",
    kind: "count",
    accessor: (s) => (
      <span className="font-mono text-xs">{s.desiredCidrs.length}</span>
    ),
    sortAccessor: (s) => s.desiredCidrs.length,
  },
];

// ─── Main page ──────────────────────────────────────────────────────────────
function SnapshotHistory({
  open,
  onToggle,
  snapshots,
}: {
  open: boolean;
  onToggle: () => void;
  snapshots: ApiserverAllowlistSnapshot[];
}) {
  return (
    <div className="rounded-sm border">
      <ActionButton
        {...BARE_BUTTON}
        onClick={onToggle}
        className="flex w-full items-center justify-between p-3 text-sm font-medium hover:bg-muted/30 whitespace-normal shrink"
      >
        <span>Snapshot history</span>
        {open ? (
          <ChevronDown className="h-4 w-4" />
        ) : (
          <ChevronRight className="h-4 w-4" />
        )}
      </ActionButton>
      {open && (
        <div className="border-t p-3">
          <DataTable
            data={snapshots}
            columns={snapshotColumns}
            keyExtractor={(s) => String(s.id)}
            density="compact"
            searchable={false}
            emptyState={{
              title: "No snapshots captured yet",
              description: "Snapshots appear here after the next reconcile.",
            }}
          />
        </div>
      )}
    </div>
  );
}

function ClusterNetworkAccessPage() {
  const params = Route.useParams();
  const clusterId = params.id;
  const queryClient = useQueryClient();
  const { canWrite, reason } = useClustersUpdate(clusterId);

  const query = useQuery({
    queryKey: queryKeys.clusterPages.apiserverAllowlist(clusterId),
    queryFn: () => getApiserverAllowlist(clusterId),
    // `network_access.changed` covers allow-list writes while the stream is
    // open; reconcile-side sync-status changes heal via this fallback poll.
    refetchInterval: liveFallback(30_000),
  });

  // Editor state — initialised from the server snapshot on first load.
  const [editing, setEditing] = useState<boolean>(false);
  const [editedCIDRs, setEditedCIDRs] = useState<string[]>([]);
  const [editedMode, setEditedMode] =
    useState<ApiserverAllowlistMode>("monitor");
  const [newCIDR, setNewCIDR] = useState<string>("");
  const [showSnapshots, setShowSnapshots] = useState<boolean>(false);
  const [confirmEnforce, setConfirmEnforce] = useState<boolean>(false);
  const [requireForce, setRequireForce] = useState<boolean>(false);

  const beginEditing = () => {
    if (!query.data) return;
    setEditedCIDRs(query.data.operatorCidrs ?? []);
    setEditedMode(query.data.mode ?? "monitor");
    setEditing(true);
  };
  const data = query.data;

  const updateMut = useMutation({
    mutationFn: (body: {
      cidrs: string[];
      mode: ApiserverAllowlistMode;
      forceApply?: boolean;
    }) => updateApiserverAllowlist(clusterId, body),
    onSuccess: () => {
      toastSuccess("Apiserver allow-list updated");
      setEditing(false);
      setRequireForce(false);
      queryClient.invalidateQueries({
        queryKey: queryKeys.clusterPages.apiserverAllowlist(clusterId),
      });
    },
    onError: (err: AxiosError<{ error?: { code?: string } }>) => {
      const code = err.response?.data?.error?.code;
      if (code === "mode_change_requires_force") {
        setRequireForce(true);
        toastWarning(
          "Enforce mode requires force_apply while drift exists — re-submit to apply anyway.",
        );
      } else {
        toastError(
          extractApiErrorMessage(err) ?? "Failed to update allow-list",
        );
      }
    },
  });

  const reconcileMut = useMutation({
    mutationFn: () => reconcileApiserverAllowlist(clusterId),
    onSuccess: () => {
      toastSuccess("Reconcile queued");
      // The reconciler runs async; refresh after a short delay.
      setTimeout(
        () =>
          queryClient.invalidateQueries({
            queryKey: queryKeys.clusterPages.apiserverAllowlist(clusterId),
          }),
        2_000,
      );
    },
    onError: (err: unknown) => {
      toastError(extractApiErrorMessage(err) ?? "Failed to queue reconcile");
    },
  });

  const { data: snapshotPage } = useQuery({
    queryKey: queryKeys.clusterPages.apiserverAllowlistSnapshots(clusterId),
    queryFn: () => listApiserverAllowlistSnapshots(clusterId, { limit: 20 }),
    enabled: showSnapshots,
  });
  const snapshots = snapshotPage?.data ?? [];

  function handleSave() {
    if (
      data?.mode === "monitor" &&
      editedMode === "enforce" &&
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
      mode: "enforce",
      forceApply: true,
    });
  }

  function handleAddCIDR() {
    const trimmed = newCIDR.trim();
    if (!trimmed) return;
    if (editedCIDRs.includes(trimmed)) {
      toastInfo("CIDR already in list");
      return;
    }
    setEditedCIDRs([...editedCIDRs, trimmed]);
    setNewCIDR("");
  }

  if (query.isLoading || query.isError || !data)
    return (
      <QueryStates query={query} permission="clusters:read">
        {() => null}
      </QueryStates>
    );

  const canMonitor = data.capability.canMonitor;
  const canEnforce = data.capability.canEnforce;
  const canReconcile = canWrite && canMonitor;

  return (
    <PageShell>
      {/* Header */}
      <PageHeader
        title="Network &amp; access"
        status={<ModeBadge mode={data.mode} drift={data.drift} />}
        description="Manage the operator-defined CIDR allow-list for this cluster's apiserver. Astronomer's tunnel egress block is always stamped on top — operators can't remove it without disabling Astronomer management."
        actions={
          <>
            {data.drift && (
              <span className="inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs bg-status-warning/10 text-status-warning">
                <ShieldAlert className="h-3 w-3" /> Drift detected
              </span>
            )}
            {data.syncStatus === "synced" && (
              <span className="inline-flex items-center gap-1 rounded-sm px-2 py-0.5 text-xs bg-status-success/10 text-status-success">
                <ShieldCheck className="h-3 w-3" /> Synced
              </span>
            )}
            <ActionButton
              {...BARE_BUTTON}
              tooltip={
                !canWrite
                  ? undefined
                  : canMonitor
                    ? "Run reconcile now"
                    : undefined
              }
              disabledReason={
                !canWrite
                  ? reason
                  : canMonitor
                    ? undefined
                    : (data.capability.reason ??
                      "This provider cannot be monitored")
              }
              onClick={() => reconcileMut.mutate()}
              disabled={!canReconcile || reconcileMut.isPending}
              className="inline-flex items-center gap-1 rounded-sm border px-3 py-1 text-sm hover:bg-muted/30 disabled:opacity-50 font-normal"
            >
              <RefreshCw
                className={
                  reconcileMut.isPending ? "h-4 w-4 animate-spin" : "h-4 w-4"
                }
              />
              Reconcile now
            </ActionButton>
          </>
        }
      />

      {!canEnforce && (
        <div className="flex items-start gap-2 rounded-sm border border-status-warning/30 bg-status-warning/10 p-3 text-sm text-status-warning">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
          <div>
            <div className="font-medium">Provider enforcement unavailable</div>
            <div className="text-xs">
              {data.capability.reason ??
                "Astronomer cannot enforce API-server access for this provider."}
              {data.capability.requiredMetadata.length > 0 && (
                <>
                  {" "}
                  Required metadata:{" "}
                  {data.capability.requiredMetadata.join(", ")}.
                </>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Detected provider + status row */}
      <div className="grid grid-cols-3 gap-4 rounded-sm border bg-muted/30 p-4 text-sm">
        <div>
          <div className="text-muted-foreground">Detected provider</div>
          <div className="font-mono">{data.detectedProvider}</div>
        </div>
        <div>
          <div className="text-muted-foreground">Sync status</div>
          <div className="font-mono">{data.syncStatus}</div>
        </div>
        <div>
          <div className="text-muted-foreground">Last reconciled</div>
          <div className="font-mono">{data.lastReconciledAt ?? "—"}</div>
        </div>
        {data.lastError && (
          <div className="col-span-3 flex items-start gap-2 rounded-sm bg-status-warning/10 p-2 text-xs text-status-warning">
            <AlertTriangle className="mt-0.5 h-3 w-3" />
            <span>{data.lastError}</span>
          </div>
        )}
      </div>

      {/* CIDR lists side-by-side */}
      <div className="grid grid-cols-2 gap-4">
        <div className="rounded-sm border p-4">
          <div className="flex items-center justify-between mb-2">
            <h2 className="font-medium">Operator CIDRs</h2>
            {!editing ? (
              <ActionButton
                {...BARE_BUTTON}
                tooltip={
                  !canWrite ? undefined : canMonitor ? "Edit" : undefined
                }
                disabledReason={
                  !canWrite
                    ? reason
                    : canMonitor
                      ? undefined
                      : data.capability.reason
                }
                onClick={beginEditing}
                disabled={!canWrite || !canMonitor}
                className="text-xs underline disabled:opacity-50 inline-block font-normal"
              >
                Edit
              </ActionButton>
            ) : (
              <div className="flex gap-2">
                <ActionButton
                  {...BARE_BUTTON}
                  onClick={() => {
                    setEditing(false);
                    setEditedCIDRs(data.operatorCidrs);
                    setEditedMode(data.mode);
                    setRequireForce(false);
                  }}
                  className="text-xs underline inline-block font-normal"
                >
                  Cancel
                </ActionButton>
                <ActionButton
                  {...BARE_BUTTON}
                  onClick={handleSave}
                  disabled={updateMut.isPending}
                  className="text-xs underline text-status-info inline-block font-normal"
                >
                  Save
                </ActionButton>
              </div>
            )}
          </div>
          <div className="flex flex-wrap gap-1 min-h-8">
            {(editing ? editedCIDRs : data.operatorCidrs).map((c) => (
              <CIDRPill
                key={c}
                cidr={c}
                removable={editing}
                onRemove={() =>
                  setEditedCIDRs(editedCIDRs.filter((x) => x !== c))
                }
              />
            ))}
            {(editing ? editedCIDRs : data.operatorCidrs).length === 0 && (
              <span className="text-xs text-muted-foreground">
                No operator CIDRs configured.
              </span>
            )}
          </div>
          {editing && (
            <div className="mt-3 flex gap-2">
              <Input
                type="text"
                value={newCIDR}
                onChange={(e) => setNewCIDR(e.target.value)}
                placeholder="e.g. 10.0.0.0/8"
                className="flex-1 rounded-sm border px-2 py-1 text-sm font-mono"
              />
              <ActionButton
                {...BARE_BUTTON}
                onClick={handleAddCIDR}
                className="rounded-sm border px-3 py-1 text-sm hover:bg-muted/30 inline-block font-normal"
              >
                Add
              </ActionButton>
            </div>
          )}
        </div>

        <div className="rounded-sm border bg-muted/30 p-4">
          <h2 className="font-medium mb-2 flex items-center gap-1">
            Astronomer egress
            <Tooltip content="Astronomer's tunnel egress IPs are stamped onto every cluster's allow-list automatically. Operators cannot remove this block — doing so would brick the tunnel.">
              <span className="text-xs text-muted-foreground">ⓘ</span>
            </Tooltip>
          </h2>
          <div className="flex flex-wrap gap-1 min-h-8">
            {data.astronomerEgress.length === 0 ? (
              <span className="text-xs text-muted-foreground">
                No egress CIDRs configured.
              </span>
            ) : (
              data.astronomerEgress.map((c) => <CIDRPill key={c} cidr={c} />)
            )}
          </div>
        </div>
      </div>

      {/* Mode toggle */}
      <div className="rounded-sm border p-4">
        <h2 className="font-medium mb-2">Mode</h2>
        <div className="flex gap-3 text-sm">
          {(["monitor", "enforce", "disabled"] as const).map((m) => (
            <label key={m} className="flex items-center gap-1">
              <Input
                type="radio"
                name="mode"
                value={m}
                checked={(editing ? editedMode : data.mode) === m}
                onChange={() => setEditedMode(m)}
                disabled={
                  !editing ||
                  !canWrite ||
                  (m === "monitor" && !canMonitor) ||
                  (m === "enforce" && !canEnforce)
                }
              />
              <span className="capitalize">{m}</span>
            </label>
          ))}
        </div>
        <p className="mt-2 text-xs text-muted-foreground">
          <strong>monitor</strong>: record drift, never patch.{" "}
          <strong>enforce</strong>: patch the cloud LB / firewall on every
          divergence. <strong>disabled</strong>: no reconciliation.
        </p>
      </div>

      {/* Effective list */}
      <div className="rounded-sm border p-4">
        <h2 className="font-medium mb-2">Effective (last reconcile)</h2>
        <div className="flex flex-wrap gap-1 min-h-8">
          {data.effective.length === 0 ? (
            <span className="text-xs text-muted-foreground">
              No effective list captured yet — reconcile to populate.
            </span>
          ) : (
            data.effective.map((c) => <CIDRPill key={c} cidr={c} />)
          )}
        </div>
      </div>

      {/* Desired (preview) */}
      <div className="rounded-sm border p-4">
        <h2 className="font-medium mb-2 flex items-center gap-1">
          Desired
          <CheckCircle2 className="h-3 w-3 text-status-success" />
        </h2>
        <div className="flex flex-wrap gap-1 min-h-8">
          {data.desired.map((c) => (
            <CIDRPill key={c} cidr={c} />
          ))}
        </div>
      </div>

      {/* Snapshot history */}
      <SnapshotHistory
        open={showSnapshots}
        onToggle={() => setShowSnapshots(!showSnapshots)}
        snapshots={snapshots}
      />

      {/* Enforce confirm modal */}
      <ConfirmDialog
        open={confirmEnforce}
        title="Switch to enforce mode?"
        description={
          "Switching to enforce will patch the cloud LB / firewall on the next reconcile. " +
          "If drift exists this can lock out a CIDR that's currently allowed but not in your operator list."
        }
        confirmText="Apply anyway (force)"
        onConfirm={handleEnforceConfirm}
        onClose={() => setConfirmEnforce(false)}
      />
    </PageShell>
  );
}

export const Route = createFileRoute("/dashboard/clusters/$id/network-access/")(
  {
    component: ClusterNetworkAccessPage,
  },
);
