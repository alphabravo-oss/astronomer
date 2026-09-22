import { Shield, Stethoscope } from "lucide-react";
import { StatusBadge } from "@/components/ui/status-badge";
import { cn, formatRelativeTime } from "@/lib/utils";
import type { ClusterAgentItem } from "@/types";
import type { Column } from "@/components/ui/data-table";

export function agentColumns(
  setSelectedClusterId: (id: string) => void,
  setUpgradePlan: (plan: null) => void,
): Column<ClusterAgentItem>[] {
  return [
    {
      key: "cluster",
      header: "Cluster",
      accessor: (row) => (
        <div>
          <p className="font-medium text-foreground">
            {row.clusterDisplayName || row.clusterName}
          </p>
          <p className="text-xs text-muted-foreground font-mono">
            {row.clusterId}
          </p>
        </div>
      ),
      sortAccessor: (row) => row.clusterDisplayName || row.clusterName,
    },
    {
      key: "agentStatus",
      header: "Agent",
      accessor: (row) => (
        <div className="space-y-1">
          <StatusBadge
            status={row.agentStatus}
            label={capitalize(row.agentStatus)}
          />
          {row.degradedReasons?.length ? (
            <p className="text-xs text-status-warning">
              {row.degradedReasons[0]}
            </p>
          ) : null}
        </div>
      ),
      sortAccessor: (row) => row.agentStatus,
    },
    {
      key: "version",
      header: "Version",
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
      accessor: (row) => (
        <span
          title={row.compatibilityMessage}
          className={cn(
            "inline-flex items-center rounded-sm border px-1.5 py-0.5 text-xs font-medium",
            compatibilityTone(row.compatibilityStatus),
          )}
        >
          {compatibilityLabel(row.compatibilityStatus)}
        </span>
      ),
      sortAccessor: (row) => row.compatibilityStatus,
    },
    {
      key: "profile",
      header: "Profile",
      accessor: (row) => (
        <span
          className={cn(
            "inline-flex items-center gap-1 rounded-sm px-1.5 py-0.5 text-xs font-medium",
            row.privilegeProfile === "admin"
              ? "bg-status-warning/10 text-status-warning"
              : "bg-muted text-muted-foreground",
          )}
        >
          <Shield className="h-3 w-3" />
          {row.privilegeProfile}
        </span>
      ),
      sortAccessor: (row) => row.privilegeProfile,
    },
    {
      key: "capabilities",
      header: "Capabilities",
      accessor: (row) => (
        <div className="flex flex-wrap gap-1">
          {Object.entries(row.capabilities)
            .filter(([, enabled]) => enabled)
            .slice(0, 4)
            .map(([name]) => (
              <span
                key={name}
                className="rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground"
              >
                {name.replace("_", " ")}
              </span>
            ))}
        </div>
      ),
      sortable: false,
    },
    {
      key: "kubernetes",
      header: "Kubernetes",
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
      header: "Last Heartbeat",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {row.lastHeartbeat ? formatRelativeTime(row.lastHeartbeat) : "-"}
        </span>
      ),
      sortAccessor: (row) => row.lastHeartbeat || "",
    },
    {
      key: "session",
      header: "Session",
      accessor: (row) => (
        <div className="text-xs text-muted-foreground">
          <p className="font-mono">{row.agentId || "-"}</p>
          {row.podName ? <p>{row.podName}</p> : null}
        </div>
      ),
      sortAccessor: (row) => row.agentId || "",
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <button
          onClick={(event) => {
            event.stopPropagation();
            setSelectedClusterId(row.clusterId);
            setUpgradePlan(null);
          }}
          className="inline-flex items-center gap-1.5 rounded-md border border-border px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
        >
          <Stethoscope className="h-3.5 w-3.5" />
          Diagnostics
        </button>
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
