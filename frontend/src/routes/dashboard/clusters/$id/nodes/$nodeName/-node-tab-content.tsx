import type { NodeDetail } from "@/types";
import type { NodeActions } from "./-node-actions";
import { OverviewTab } from "./-node-overview-tab";
import { InfoTab } from "./-node-info-tab";
import { TaintsTab } from "./-node-taints-tab";
import {
  PodsTab,
  ConditionsTab,
  ImagesTab,
  EventsTab,
} from "./-node-workload-tabs";

export type NodeTabId =
  | "overview"
  | "pods"
  | "conditions"
  | "info"
  | "taints"
  | "images"
  | "events";

/**
 * Dispatches the active node-detail tab to its content component. Extracted
 * from the route body so the latter stays under the function-length budget.
 */
export function NodeTabContent({
  activeTab,
  node,
  actions,
  canUpdate,
  blockedReason,
}: {
  activeTab: NodeTabId;
  node: NodeDetail;
  actions: NodeActions;
  canUpdate: boolean;
  blockedReason?: string;
}) {
  switch (activeTab) {
    case "overview":
      return (
        <OverviewTab
          node={node}
          canUpdate={canUpdate}
          blockedReason={blockedReason}
          addLabelPending={actions.addLabelPending}
          removeLabelPending={actions.removeLabelPending}
          addAnnotationPending={actions.addAnnotationPending}
          removeAnnotationPending={actions.removeAnnotationPending}
          onOpenAddLabel={() => actions.setShowAddLabel(true)}
          onRemoveLabel={actions.handleRemoveLabel}
          onOpenAddAnnotation={() => actions.setShowAddAnnotation(true)}
          onRemoveAnnotation={actions.handleRemoveAnnotation}
        />
      );
    case "pods":
      return <PodsTab pods={node.pods} />;
    case "conditions":
      return <ConditionsTab conditions={node.conditions} />;
    case "info":
      return <InfoTab nodeInfo={node.nodeInfo} />;
    case "taints":
      return (
        <TaintsTab
          taints={node.taints}
          canUpdate={canUpdate}
          blockedReason={blockedReason}
          addTaintPending={actions.addTaintPending}
          removeTaintPending={actions.removeTaintPending}
          onOpenAddTaint={() => actions.setShowAddTaint(true)}
          onRemoveTaint={actions.handleRemoveTaint}
        />
      );
    case "images":
      return <ImagesTab images={node.images} />;
    case "events":
      return <EventsTab events={node.events} />;
    default:
      return null;
  }
}
