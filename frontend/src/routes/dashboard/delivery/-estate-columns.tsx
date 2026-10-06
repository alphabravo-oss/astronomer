import { Shield } from "lucide-react";
import { AgeCell } from "@/components/ui/age-cell";
import { StackedCell } from "@/components/ui/stacked-cell";
import { type Column } from "@/components/ui/data-table";
import { DeliveryPhaseBadge } from "@/components/delivery/shared";
import { type DeliveryEstateCluster } from "@/lib/api/delivery-system";

export const estateColumns: Column<DeliveryEstateCluster>[] = [
  {
    key: "cluster",
    header: "Cluster",
    kind: "name",
    minSize: 180,
    accessor: (row) => (
      <StackedCell
        primary={row.displayName || row.name}
        secondary={row.name}
        secondaryMono
      />
    ),
    sortAccessor: (row) => row.displayName || row.name,
  },
  {
    key: "environment",
    header: "Environment",
    kind: "badge",
    size: 133,
    accessor: (row) => (
      <span className="text-xs capitalize text-muted-foreground">
        {row.environment || "—"}
      </span>
    ),
    sortAccessor: (row) => row.environment || "",
  },
  {
    key: "role",
    header: "Role",
    kind: "text",
    size: 120,
    minSize: 84,
    accessor: (row) =>
      row.isLocal ? (
        <span className="text-xs text-muted-foreground">Local host-only</span>
      ) : (
        <span className="inline-flex items-center gap-1 text-xs">
          <Shield className="h-3 w-3" />
          {row.privilegeProfile}
        </span>
      ),
    sortAccessor: (row) => (row.isLocal ? "local" : row.privilegeProfile),
  },
  {
    key: "agent",
    header: "Agent",
    kind: "status",
    size: 124,
    accessor: (row) => (
      <DeliveryPhaseBadge
        value={
          row.connected ? (row.stale ? "stale" : "connected") : "disconnected"
        }
      />
    ),
    sortAccessor: (row) =>
      row.connected ? (row.stale ? "stale" : "connected") : "disconnected",
  },
  {
    key: "flux",
    header: "Flux",
    kind: "status",
    size: 130,
    accessor: (row) => (
      <div className="min-w-0 space-y-1">
        <DeliveryPhaseBadge value={row.compatibilityStatus} />
        <StackedCell
          primary={row.fluxVersion || "—"}
          primaryClassName="font-mono text-xs font-normal text-muted-foreground"
        />
      </div>
    ),
    sortAccessor: (row) => row.compatibilityStatus,
  },
  {
    key: "assignments",
    header: "Assignments",
    kind: "count",
    size: 150,
    maxSize: 200,
    accessor: (row) => (
      <StackedCell
        primary={`${row.readyCount}/${row.assignmentCount}`}
        primaryClassName="tabular-nums text-sm"
        secondary={[
          row.failedCount > 0 ? `${row.failedCount} failed` : "",
          row.driftedCount > 0 ? `${row.driftedCount} drifted` : "",
        ]
          .filter(Boolean)
          .join(" · ")}
      />
    ),
    sortAccessor: (row) => row.failedCount * 1000 + row.assignmentCount,
  },
  {
    key: "heartbeat",
    header: "Heartbeat",
    kind: "age",
    size: 119,
    accessor: (row) => <AgeCell value={row.lastHeartbeat} />,
    sortAccessor: (row) => row.lastHeartbeat ?? "",
  },
];
