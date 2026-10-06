import { DataTable, type Column } from "@/components/ui/data-table";
import { ChevronDown, ChevronRight, Lock } from "lucide-react";
import {
  type ApiserverAllowlistMode,
  type ApiserverAllowlistSnapshot,
} from "@/lib/api/cluster-apiserver-allowlist";
import { ActionButton } from "@/components/ui/action-button";
import { BARE_BUTTON } from "@/lib/bare-button";

// ─── Mode badge ─────────────────────────────────────────────────────────────
export function ModeBadge({
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
export function CIDRPill({
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
export const snapshotColumns: Column<ApiserverAllowlistSnapshot>[] = [
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
export function SnapshotHistory({
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
