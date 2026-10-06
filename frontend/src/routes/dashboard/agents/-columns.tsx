import { Stethoscope } from "lucide-react";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn } from "@/lib/utils";
import {
  ChipsCell,
  EntityCell,
  StatusReasonCell,
  TimestampCell,
} from "@/components/tables/cells";
import type { ClusterAgentItem } from "@/types";
import type { Column } from "@/components/ui/data-table";
import { Tooltip } from "@/components/ui/tooltip";
import { BareButton } from "@/components/form/bare-button";

export function agentColumns(
  setSelectedClusterId: (id: string) => void,
  setUpgradePlan: (plan: null) => void,
): Column<ClusterAgentItem>[] {
  return [
    {
      key: "cluster",
      header: "Cluster",
      kind: "name",
      minSize: 220,
      pin: "start",
      accessor: (row) => (
        <EntityCell
          primary={row.clusterDisplayName || row.clusterName}
          secondary={<span className="font-mono">{row.clusterId}</span>}
        />
      ),
      sortAccessor: (row) => row.clusterDisplayName || row.clusterName,
    },
    {
      key: "agentStatus",
      header: "Agent",
      kind: "status",
      size: 160,
      minSize: 140,
      maxSize: 240,
      accessor: (row) => (
        <StatusReasonCell
          status={
            <StatusBadge
              status={row.agentStatus}
              label={capitalize(row.agentStatus)}
            />
          }
          reason={row.degradedReasons?.[0]}
        />
      ),
      sortAccessor: (row) => row.agentStatus,
    },
    {
      key: "version",
      header: "Version",
      kind: "version",
      accessor: (row) => (
        <span className="font-mono text-xs text-muted-foreground">
          {row.agentVersion || "-"}
        </span>
      ),
      sortAccessor: (row) => row.agentVersion || "",
    },
    {
      key: "compatibility",
      header: "Compatibility",
      kind: "badge",
      size: 150,
      accessor: (row) => (
        <Tooltip content={row.compatibilityMessage}>
          <span
            className={cn(
              "inline-flex items-center rounded-sm border px-1.5 py-0.5 text-xs font-medium",
              compatibilityTone(row.compatibilityStatus),
            )}
          >
            {compatibilityLabel(row.compatibilityStatus)}
          </span>
        </Tooltip>
      ),
      sortAccessor: (row) => row.compatibilityStatus,
    },
    {
      key: "capabilities",
      header: "Capabilities",
      kind: "badge",
      size: 220,
      minSize: 180,
      maxSize: 280,
      accessor: (row) => (
        <ChipsCell
          items={Object.entries(row.capabilities)
            .filter(([, enabled]) => enabled)
            .map(([name]) => name.replaceAll("_", " "))}
        />
      ),
      sortable: false,
    },
    {
      key: "kubernetes",
      header: "Kubernetes",
      kind: "version",
      size: 130,
      accessor: (row) => (
        <div className="text-xs text-muted-foreground">
          <p className="font-mono">{row.kubernetesVersion || "-"}</p>
          <p>{row.nodeCount} nodes</p>
        </div>
      ),
      sortAccessor: (row) => row.kubernetesVersion || "",
    },
    {
      key: "lastHeartbeat",
      header: "Heartbeat",
      kind: "age",
      size: 130,
      accessor: (row) => (
        <TimestampCell
          value={row.lastHeartbeat}
          fallback="-"
          className="text-xs text-muted-foreground"
        />
      ),
      sortAccessor: (row) => row.lastHeartbeat || "",
    },
    {
      key: "session",
      header: "Session",
      kind: "text",
      size: 200,
      minSize: 160,
      accessor: (row) => (
        <EntityCell
          mono
          primaryClassName="font-normal text-muted-foreground"
          primary={row.agentId || "-"}
          secondary={row.podName}
        />
      ),
      sortAccessor: (row) => row.agentId || "",
    },
    {
      key: "actions",
      header: "",
      kind: "actions",
      size: 130,
      minSize: 130,
      maxSize: 140,
      accessor: (row) => (
        <BareButton
          onClick={(event) => {
            event.stopPropagation();
            setSelectedClusterId(row.clusterId);
            setUpgradePlan(null);
          }}
          className="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <Stethoscope className="h-3.5 w-3.5" />
          Diagnostics
        </BareButton>
      ),
      sortable: false,
    },
  ];
}

export function capitalize(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

export function compatibilityLabel(value: string): string {
  if (value === "supported") return "Supported";
  if (value === "deprecated") return "Deprecated";
  if (value === "blocked") return "Blocked";
  if (value === "unknown") return "Unknown";
  return capitalize(value || "unknown");
}

export function compatibilityTone(value: string): string {
  if (value === "supported")
    return "border-status-success/30 bg-status-success/10 text-status-success";
  if (value === "blocked")
    return "border-status-error/30 bg-status-error/10 text-status-error";
  if (value === "deprecated")
    return "border-status-warning/30 bg-status-warning/10 text-status-warning";
  return "border-border bg-muted/30 text-muted-foreground";
}
