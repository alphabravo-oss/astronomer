import { Code, ShieldBan, ShieldCheck, Unplug } from "lucide-react";
import { ActionButton } from "@/components/ui/action-button";
import { ResourceActions } from "@/components/workloads/resource-actions";
import type { PermissionDecision } from "@/lib/permissions";

/**
 * The node masthead's action cluster: View YAML, Cordon/Uncordon, Drain, and
 * the shared ResourceActions delete menu. Extracted so the page body stays
 * under the function-length budget.
 */
export function NodeHeaderActions({
  clusterId,
  nodeName,
  unschedulable,
  onViewYaml,
  onCordon,
  onUncordon,
  onDrainClick,
  onDeleted,
  cordonPending,
  uncordonPending,
  drainPending,
  nodeUpdateDecision,
  nodeManageDecision,
}: {
  clusterId: string;
  nodeName: string;
  unschedulable: boolean;
  onViewYaml: () => void;
  onCordon: () => void;
  onUncordon: () => void;
  onDrainClick: () => void;
  onDeleted: () => void;
  cordonPending: boolean;
  uncordonPending: boolean;
  drainPending: boolean;
  nodeUpdateDecision: PermissionDecision;
  nodeManageDecision: PermissionDecision;
}) {
  const nodeUpdateBlockedReason = nodeUpdateDecision.allowed
    ? undefined
    : nodeUpdateDecision.disabledReason;
  const nodeManageBlockedReason = nodeManageDecision.allowed
    ? undefined
    : nodeManageDecision.disabledReason;

  return (
    <>
      <ActionButton
        onClick={onViewYaml}
        size="sm"
        icon={<Code className="h-3.5 w-3.5" />}
      >
        View YAML
      </ActionButton>
      {unschedulable ? (
        <ActionButton
          onClick={onUncordon}
          disabled={uncordonPending || !nodeUpdateDecision.allowed}
          disabledReason={nodeUpdateBlockedReason}
          size="sm"
          icon={<ShieldCheck className="h-3.5 w-3.5" />}
          className="gap-1.5 text-sm border-status-success/30 text-status-success hover:bg-status-success/10"
        >
          Uncordon
        </ActionButton>
      ) : (
        <ActionButton
          onClick={onCordon}
          disabled={cordonPending || !nodeUpdateDecision.allowed}
          disabledReason={nodeUpdateBlockedReason}
          size="sm"
          icon={<ShieldBan className="h-3.5 w-3.5" />}
          className="gap-1.5 text-sm border-status-warning/30 text-status-warning hover:bg-status-warning/10"
        >
          Cordon
        </ActionButton>
      )}
      <ActionButton
        onClick={onDrainClick}
        disabled={drainPending || !nodeManageDecision.allowed}
        disabledReason={nodeManageBlockedReason}
        size="sm"
        icon={<Unplug className="h-3.5 w-3.5" />}
        className="gap-1.5 text-sm border-status-error/30 text-status-error hover:bg-status-error/10"
      >
        Drain
      </ActionButton>
      {/* Node is cluster-scoped — ResourceActions renders only Delete here. */}
      <ResourceActions
        clusterId={clusterId}
        kind="Node"
        name={nodeName}
        onDeleted={onDeleted}
      />
    </>
  );
}
