import { Cpu, MemoryStick, Box, CheckCircle2, XCircle, Tag, Code, Plus } from "lucide-react";
import { cn, formatBytes, formatCPU } from "@/lib/utils";
import type { NodeDetail } from "@/types";

function ResourceGauge({
  label,
  icon: Icon,
  used,
  total,
  formatFn,
}: {
  label: string;
  icon: React.ElementType;
  used: number;
  total: number;
  formatFn: (v: number) => string;
}) {
  const pct = total > 0 ? (used / total) * 100 : 0;
  const color =
    pct >= 90
      ? "bg-status-error"
      : pct >= 75
        ? "bg-status-warning"
        : "bg-status-success";
  const textColor =
    pct >= 90
      ? "text-status-error"
      : pct >= 75
        ? "text-status-warning"
        : "text-status-success";

  return (
    <div className="bg-card border border-border rounded-lg p-4">
      <div className="flex items-center gap-2 mb-3">
        <Icon className="h-4 w-4 text-muted-foreground" />
        <span className="text-sm font-medium text-foreground">{label}</span>
      </div>
      <div className="flex items-end gap-2 mb-2">
        <span className={cn("text-2xl font-bold tabular-nums", textColor)}>
          {Math.round(pct)}%
        </span>
      </div>
      <div className="w-full h-2 bg-muted rounded-full overflow-hidden mb-2">
        <div
          className={cn("h-full rounded-full transition-all", color)}
          style={{ width: `${Math.min(pct, 100)}%` }}
        />
      </div>
      <p className="text-xs text-muted-foreground tabular-nums">
        {formatFn(used)} / {formatFn(total)}
      </p>
    </div>
  );
}

function ConditionAlert({ label, ok }: { label: string; ok: boolean }) {
  return (
    <div
      className={cn(
        "flex items-center gap-2 px-3 py-2 rounded-md border text-xs font-medium",
        ok
          ? "bg-status-success/5 border-status-success/20 text-status-success"
          : "bg-status-error/5 border-status-error/20 text-status-error",
      )}
    >
      {ok ? (
        <CheckCircle2 className="h-3.5 w-3.5" />
      ) : (
        <XCircle className="h-3.5 w-3.5" />
      )}
      {label}
    </div>
  );
}

export function OverviewTab({
  node,
  canUpdate,
  blockedReason,
  addLabelPending,
  removeLabelPending,
  addAnnotationPending,
  removeAnnotationPending,
  onOpenAddLabel,
  onRemoveLabel,
  onOpenAddAnnotation,
  onRemoveAnnotation,
}: {
  node: NodeDetail;
  canUpdate: boolean;
  blockedReason?: string;
  addLabelPending: boolean;
  removeLabelPending: boolean;
  addAnnotationPending: boolean;
  removeAnnotationPending: boolean;
  onOpenAddLabel: () => void;
  onRemoveLabel: (key: string) => void;
  onOpenAddAnnotation: () => void;
  onRemoveAnnotation: (key: string) => void;
}) {
  const condMap = Object.fromEntries(
    node.conditions.map((c) => [c.type, c.status]),
  );
  const isKubeletOk = condMap["Ready"] === "True";
  const isMemoryPressureOk = condMap["MemoryPressure"] === "False";
  const isDiskPressureOk = condMap["DiskPressure"] === "False";
  const isPidPressureOk = condMap["PIDPressure"] === "False";

  return (
    <div className="space-y-6">
      {/* Health Status Alerts */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <ConditionAlert label="Kubelet" ok={isKubeletOk} />
        <ConditionAlert label="Memory Pressure" ok={isMemoryPressureOk} />
        <ConditionAlert label="Disk Pressure" ok={isDiskPressureOk} />
        <ConditionAlert label="PID Pressure" ok={isPidPressureOk} />
      </div>

      {/* Resource Gauges */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
        <ResourceGauge
          label="CPU"
          icon={Cpu}
          used={node.cpuUsage}
          total={node.cpuCapacity}
          formatFn={formatCPU}
        />
        <ResourceGauge
          label="Memory"
          icon={MemoryStick}
          used={node.memoryUsage}
          total={node.memoryCapacity}
          formatFn={formatBytes}
        />
        <ResourceGauge
          label="Pods"
          icon={Box}
          used={node.podCount}
          total={node.podCapacity}
          formatFn={(v) => String(v)}
        />
      </div>

      {/* Addresses */}
      {node.addresses.length > 0 && (
        <div className="bg-card border border-border rounded-lg p-4">
          <h3 className="text-sm font-medium text-foreground mb-3">
            Addresses
          </h3>
          <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
            {node.addresses.map((addr) => (
              <div
                key={`${addr.type}-${addr.address}`}
                className="flex items-center gap-2"
              >
                <span className="px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground min-w-[80px] text-center">
                  {addr.type}
                </span>
                <span className="text-xs font-mono text-foreground">
                  {addr.address}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Labels */}
      <div className="bg-card border border-border rounded-lg p-4">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <Tag className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-sm font-medium text-foreground">Labels</h3>
            <span className="text-xs text-muted-foreground">
              ({Object.keys(node.labels).length})
            </span>
          </div>
          <button
            onClick={onOpenAddLabel}
            disabled={addLabelPending || !canUpdate}
            title={blockedReason}
            aria-label="Add label"
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Plus className="h-3 w-3" /> Add
          </button>
        </div>
        <div className="flex flex-wrap gap-1.5">
          {Object.entries(node.labels).map(([k, v]) => (
            <span
              key={k}
              className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-2xs bg-muted text-muted-foreground font-mono group"
            >
              <span className="text-foreground">{k}</span>
              {v && <span>= {v}</span>}
              <button
                disabled={removeLabelPending || !canUpdate}
                title={blockedReason}
                onClick={() => onRemoveLabel(k)}
                className="ml-0.5 opacity-0 group-hover:opacity-100 text-status-error/70 hover:text-status-error transition-opacity disabled:cursor-not-allowed"
              >
                <XCircle className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      </div>

      {/* Annotations */}
      <div className="bg-card border border-border rounded-lg p-4">
        <div className="flex items-center justify-between mb-3">
          <div className="flex items-center gap-2">
            <Code className="h-4 w-4 text-muted-foreground" />
            <h3 className="text-sm font-medium text-foreground">
              Annotations
            </h3>
            <span className="text-xs text-muted-foreground">
              ({Object.keys(node.annotations).length})
            </span>
          </div>
          <button
            onClick={onOpenAddAnnotation}
            disabled={addAnnotationPending || !canUpdate}
            title={blockedReason}
            aria-label="Add annotation"
            className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-xs font-medium
              text-muted-foreground hover:text-foreground hover:bg-accent transition-colors disabled:cursor-not-allowed disabled:opacity-50"
          >
            <Plus className="h-3 w-3" /> Add
          </button>
        </div>
        <div className="flex flex-wrap gap-1.5">
          {Object.entries(node.annotations).map(([k, v]) => (
            <span
              key={k}
              className="inline-flex items-center gap-1 px-2 py-1 rounded-sm text-2xs bg-muted text-muted-foreground font-mono group"
            >
              <span className="text-foreground">{k}</span>
              {v && <span>= {v}</span>}
              <button
                disabled={removeAnnotationPending || !canUpdate}
                title={blockedReason}
                onClick={() => onRemoveAnnotation(k)}
                className="ml-0.5 opacity-0 group-hover:opacity-100 text-status-error/70 hover:text-status-error transition-opacity disabled:cursor-not-allowed"
              >
                <XCircle className="h-3 w-3" />
              </button>
            </span>
          ))}
        </div>
      </div>
    </div>
  );
}
