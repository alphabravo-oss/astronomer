import { createFileRoute, useNavigate } from "@tanstack/react-router";
import { useTabParam } from "@/lib/use-tab-param";
import { useState } from "react";
import { useNodeDetail } from "@/lib/hooks/clusters";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { YamlViewDialog } from "@/components/ui/yaml-view-dialog";
import { QueryStates } from "@/components/ui/query-states";
import { ResourceMasthead } from "@/components/ui/page";
import { TabStrip } from "@/components/ui/tabs";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { formatRelativeTime } from "@/lib/utils";
import { Server } from "lucide-react";
import { NodeHeaderActions } from "./-node-header-actions";
import { NodeTabContent, type NodeTabId } from "./-node-tab-content";
import { NodeMetadataModals } from "./-node-metadata-modals";
import { useNodeActions } from "./-node-actions";

// ── Tabs ──

const TABS = [
  { id: "overview", label: "Overview" },
  { id: "pods", label: "Pods" },
  { id: "conditions", label: "Conditions" },
  { id: "info", label: "Info" },
  { id: "taints", label: "Taints" },
  { id: "images", label: "Images" },
  { id: "events", label: "Events" },
] as const satisfies { id: NodeTabId; label: string }[];

// ── Main Page ──

function NodeDetailPage() {
  const params = Route.useParams();
  return <NodeDetailPageBody clusterId={params.id} nodeName={params.nodeName} />;
}

// Split from NodeDetailPage so tests can render the real page body without
// needing an active router match for Route.useParams() (the route's own
// params hook requires a live router context; the body doesn't).
export function NodeDetailPageBody({
  clusterId,
  nodeName,
}: {
  clusterId: string;
  nodeName: string;
}) {
  const navigate = useNavigate();
  const [activeTab, setActiveTab] = useTabParam<NodeTabId>(
    TABS.map((t) => t.id),
    "overview",
  );

  const nodeQuery = useNodeDetail(clusterId, nodeName);
  const { data: node, isLoading, refetch } = nodeQuery;
  const [showYaml, setShowYaml] = useState(false);
  const nodeScope = { type: "cluster" as const, id: clusterId };
  const nodeUpdateDecision = usePermissionDecision(
    "nodes",
    "update",
    nodeScope,
  );
  const nodeManageDecision = usePermissionDecision(
    "nodes",
    "manage",
    nodeScope,
  );
  const nodeUpdateBlockedReason = nodeUpdateDecision.allowed
    ? undefined
    : nodeUpdateDecision.disabledReason;

  const actions = useNodeActions({
    clusterId,
    nodeName,
    refetch,
    nodeUpdateDecision,
    nodeManageDecision,
  });

  if (isLoading || nodeQuery.isError) {
    return (
      <QueryStates
        query={nodeQuery}
        permission="nodes:read"
        notFound={
          <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
            <Server className="h-8 w-8 mb-3" />
            <p>Node not found</p>
          </div>
        }
      >
        {() => null}
      </QueryStates>
    );
  }

  if (!node) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-muted-foreground">
        <Server className="h-8 w-8 mb-3" />
        <p>Node not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <p className="sr-only" role="status" aria-live="polite">
        {actions.nodeOperation.isPending
          ? `Node operation ${actions.nodeOperation.operationState.phase}`
          : ""}
      </p>
      {/* Header */}
      <ResourceMasthead
        backTo={`/dashboard/clusters/${clusterId}/nodes`}
        backLabel="Back to nodes"
        title={node.name}
        mono
        status={
          <>
            <StatusBadge status={node.status} />
            {node.unschedulable && (
              <StatusBadge
                status="warning"
                label="Unschedulable"
                shape="square"
              />
            )}
          </>
        }
        meta={[
          { label: "Roles", value: node.roles.join(", ") },
          { label: "Age", value: formatRelativeTime(node.createdAt) },
          { label: "Version", value: node.nodeInfo.kubeletVersion },
        ]}
        actions={
          <NodeHeaderActions
            clusterId={clusterId}
            nodeName={nodeName}
            unschedulable={node.unschedulable}
            onViewYaml={() => setShowYaml(true)}
            onCordon={actions.handleCordon}
            onUncordon={actions.handleUncordon}
            onDrainClick={() => actions.setShowDrain(true)}
            onDeleted={() =>
              void navigate({ to: `/dashboard/clusters/${clusterId}/nodes` })
            }
            cordonPending={actions.cordonPending}
            uncordonPending={actions.uncordonPending}
            drainPending={actions.drainPending}
            nodeUpdateDecision={nodeUpdateDecision}
            nodeManageDecision={nodeManageDecision}
          />
        }
      />

      {/* Tabs */}
      <TabStrip
        tabs={TABS.map((tab) => {
          const count =
            tab.id === "pods"
              ? node.pods.length
              : tab.id === "taints"
                ? node.taints.length
                : tab.id === "events"
                  ? node.events.length
                  : 0;
          return {
            key: tab.id,
            label: tab.label,
            count: count > 0 ? count : undefined,
          };
        })}
        value={activeTab}
        onChange={setActiveTab}
        aria-label="Node detail"
      />

      {/* Tab Content */}
      <NodeTabContent
        activeTab={activeTab}
        node={node}
        actions={actions}
        canUpdate={nodeUpdateDecision.allowed}
        blockedReason={nodeUpdateBlockedReason}
      />

      {/* YAML Dialog */}
      <YamlViewDialog
        open={showYaml}
        onClose={() => setShowYaml(false)}
        clusterId={clusterId}
        k8sPath={k8sResourcePath("nodes", nodeName)}
        title={`Node: ${nodeName}`}
        allowEdit={nodeUpdateDecision.allowed}
        forceConflictPermission={nodeManageDecision}
      />

      {/* Drain Confirm Dialog */}
      <ConfirmDialog
        open={actions.showDrain}
        onClose={() => actions.setShowDrain(false)}
        onConfirm={actions.handleDrain}
        title="Drain Node"
        description="This will cordon the node and evict all non-DaemonSet pods. Workloads will be rescheduled to other nodes."
        confirmValue={nodeName}
        confirmText="Drain"
        variant="destructive"
        loading={actions.drainPending}
      />

      <NodeMetadataModals
        actions={actions}
        canUpdate={nodeUpdateDecision.allowed}
        blockedReason={nodeUpdateBlockedReason}
      />
    </div>
  );
}

export const Route = createFileRoute(
  "/dashboard/clusters/$id/nodes/$nodeName/",
)({
  // ?tab= deep-link (P2.4): typed passthrough — useTabParam's allowlist stays the real validator.
  validateSearch: (search: Record<string, unknown>) =>
    search as { tab?: string } & Record<string, unknown>,
  component: NodeDetailPage,
});
