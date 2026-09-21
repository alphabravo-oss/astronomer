import { useState } from "react";
import { useNodeOperation } from "@/lib/hooks/clusters";
import type { NodeOperationAction, NodeTaintRequest } from "@/lib/api/nodes";
import type { PermissionDecision } from "@/lib/permissions";
import type { NodeTaint } from "@/types";
import { toastApiError, toastSuccess, toastWarning } from "@/lib/toast";
import { OperationPartialError } from "@/lib/api/operation-polling";

interface KeyValue {
  key: string;
  value: string;
}

/**
 * All node-mutation state and handlers for the node detail page: cordon /
 * uncordon / drain, plus add/remove for taints, labels, and annotations.
 * Extracted from the route so the page body stays under the 240-line
 * function budget, and so per-action pending derives from one place.
 *
 * Per-action pending replaces a single shared "any node action in flight"
 * flag that used to disable every control together — draining a node no
 * longer greys out the label/taint editors.
 */
export function useNodeActions({
  clusterId,
  nodeName,
  refetch,
  nodeUpdateDecision,
  nodeManageDecision,
}: {
  clusterId: string;
  nodeName: string;
  refetch: () => void;
  nodeUpdateDecision: PermissionDecision;
  nodeManageDecision: PermissionDecision;
}) {
  const nodeOperation = useNodeOperation();
  const [showDrain, setShowDrain] = useState(false);
  const [showAddTaint, setShowAddTaint] = useState(false);
  const [newTaint, setNewTaint] = useState<NodeTaintRequest>({
    key: "",
    value: "",
    effect: "NoSchedule",
  });
  const [showAddLabel, setShowAddLabel] = useState(false);
  const [newLabel, setNewLabel] = useState<KeyValue>({ key: "", value: "" });
  const [showAddAnnotation, setShowAddAnnotation] = useState(false);
  const [newAnnotation, setNewAnnotation] = useState<KeyValue>({
    key: "",
    value: "",
  });

  const isActionPending = (action: NodeOperationAction) =>
    nodeOperation.isPending && nodeOperation.variables?.action === action;
  const cordonPending = isActionPending("cordon");
  const uncordonPending = isActionPending("uncordon");
  const drainPending = isActionPending("drain");
  const addTaintPending = isActionPending("add_taint");
  const removeTaintPending = isActionPending("remove_taint");
  const addLabelPending = isActionPending("set_label");
  const removeLabelPending = isActionPending("remove_label");
  const addAnnotationPending = isActionPending("set_annotation");
  const removeAnnotationPending = isActionPending("remove_annotation");

  const requireUpdate = () => {
    if (nodeUpdateDecision.allowed) return true;
    toastWarning(nodeUpdateDecision.disabledReason || "Requires nodes:update");
    return false;
  };

  const handleCordon = async () => {
    if (!requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({ clusterId, nodeName, action: "cordon" });
      refetch();
      toastSuccess("Node cordoned");
    } catch (error) {
      toastApiError("Failed to cordon node", error);
    }
  };

  const handleUncordon = async () => {
    if (!requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "uncordon",
      });
      refetch();
      toastSuccess("Node uncordoned");
    } catch (error) {
      toastApiError("Failed to uncordon node", error);
    }
  };

  const handleDrain = async () => {
    if (!nodeManageDecision.allowed) {
      toastWarning(
        nodeManageDecision.disabledReason || "Requires nodes:manage",
      );
      return;
    }
    try {
      await nodeOperation.mutateAsync({ clusterId, nodeName, action: "drain" });
      toastSuccess(`Node ${nodeName} drained`);
      setShowDrain(false);
      refetch();
    } catch (error) {
      if (error instanceof OperationPartialError) {
        const blockers =
          error.operation.errorMessage ||
          "one or more pods could not be evicted";
        toastWarning(`Drain incomplete; node remains cordoned: ${blockers}`);
      } else {
        toastApiError("Failed to drain", error);
      }
    }
  };

  const handleAddTaint = async () => {
    if (!newTaint.key || !requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "add_taint",
        body: newTaint,
      });
      refetch();
      setShowAddTaint(false);
      setNewTaint({ key: "", value: "", effect: "NoSchedule" });
      toastSuccess("Taint added");
    } catch (error) {
      toastApiError("Failed to add taint", error);
    }
  };

  const handleRemoveTaint = async (taint: NodeTaint) => {
    if (!requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "remove_taint",
        body: { key: taint.key, effect: taint.effect },
      });
      refetch();
      toastSuccess("Taint removed");
    } catch (error) {
      toastApiError("Failed to remove taint", error);
    }
  };

  const handleAddLabel = async () => {
    if (!newLabel.key || !requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "set_label",
        body: newLabel,
      });
      refetch();
      setShowAddLabel(false);
      setNewLabel({ key: "", value: "" });
      toastSuccess("Label added");
    } catch (error) {
      toastApiError("Failed to add label", error);
    }
  };

  const handleRemoveLabel = async (key: string) => {
    if (!requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "remove_label",
        body: { key },
      });
      refetch();
      toastSuccess("Label removed");
    } catch (error) {
      toastApiError("Failed to remove label", error);
    }
  };

  const handleAddAnnotation = async () => {
    if (!newAnnotation.key || !requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "set_annotation",
        body: newAnnotation,
      });
      refetch();
      setShowAddAnnotation(false);
      setNewAnnotation({ key: "", value: "" });
      toastSuccess("Annotation added");
    } catch (error) {
      toastApiError("Failed to add annotation", error);
    }
  };

  const handleRemoveAnnotation = async (key: string) => {
    if (!requireUpdate()) return;
    try {
      await nodeOperation.mutateAsync({
        clusterId,
        nodeName,
        action: "remove_annotation",
        body: { key },
      });
      refetch();
      toastSuccess("Annotation removed");
    } catch (error) {
      toastApiError("Failed to remove annotation", error);
    }
  };

  return {
    nodeOperation,
    showDrain,
    setShowDrain,
    showAddTaint,
    setShowAddTaint,
    newTaint,
    setNewTaint,
    showAddLabel,
    setShowAddLabel,
    newLabel,
    setNewLabel,
    showAddAnnotation,
    setShowAddAnnotation,
    newAnnotation,
    setNewAnnotation,
    cordonPending,
    uncordonPending,
    drainPending,
    addTaintPending,
    removeTaintPending,
    addLabelPending,
    removeLabelPending,
    addAnnotationPending,
    removeAnnotationPending,
    handleCordon,
    handleUncordon,
    handleDrain,
    handleAddTaint,
    handleRemoveTaint,
    handleAddLabel,
    handleRemoveLabel,
    handleAddAnnotation,
    handleRemoveAnnotation,
  };
}

export type NodeActions = ReturnType<typeof useNodeActions>;
