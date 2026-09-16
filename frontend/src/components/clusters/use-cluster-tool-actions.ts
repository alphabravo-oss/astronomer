import { useState, type ComponentProps } from "react";
import {
  useAdoptTool,
  useInstallTool,
  useRecoverTool,
  useUninstallTool,
} from "@/lib/hooks/tools";
import {
  permissionDeniedReason,
  toastPermissionDenied,
  usePermissionDecision,
} from "@/lib/permission-hooks";
import type { PermissionDecision } from "@/lib/permissions";
import type { ClusterTool, ClusterToolStatus, ToolOperation } from "@/types";
import type { ToolCard } from "./tool-card";
import type { ToolInstallModal } from "./tool-install-modal";
import type { ToolInstallProgress } from "./tool-install-progress";
import type { ConfirmDialog } from "@/components/ui/confirm-dialog";

type DialogKind = "install" | "uninstall" | "rollback";
type ToolDialog = { kind: DialogKind; tool: ClusterTool };

function authorize(decision: PermissionDecision): boolean {
  if (decision.allowed) return true;
  toastPermissionDenied(decision);
  return false;
}

function disabledReason(decision: PermissionDecision): string | undefined {
  return decision.allowed ? undefined : permissionDeniedReason(decision);
}

/** Owns cluster-scoped tool actions, confirmation state and operation handoff. */
export function useClusterToolActions({
  clusterId,
  clusterEnvironment,
  tools,
  statuses,
}: {
  clusterId: string;
  clusterEnvironment: string;
  tools: ClusterTool[];
  statuses: ClusterToolStatus[];
}) {
  const scope = { type: "cluster" as const, id: clusterId };
  const create = usePermissionDecision("catalog", "create", scope);
  const remove = usePermissionDecision("catalog", "delete", scope);
  const update = usePermissionDecision("catalog", "update", scope);
  const install = useInstallTool();
  const uninstall = useUninstallTool();
  const adopt = useAdoptTool();
  const recovery = useRecoverTool();
  const [dialog, setDialog] = useState<ToolDialog | null>(null);
  const [activeOperation, setActiveOperation] = useState<{
    id: string;
    name: string;
  } | null>(null);
  const statusMap = new Map(statuses.map((status) => [status.slug, status]));
  const defaultPreset = ["production", "staging", "development"].includes(
    clusterEnvironment,
  )
    ? clusterEnvironment
    : "development";
  const decisionFor = (kind: DialogKind) =>
    kind === "install" ? create : kind === "uninstall" ? remove : update;
  const closeDialog = () => setDialog(null);

  function openDialog(kind: DialogKind, slug: string) {
    if (!authorize(decisionFor(kind))) return;
    const tool = tools.find((item) => item.slug === slug);
    if (tool) setDialog({ kind, tool });
  }

  function trackOperation(tool: ClusterTool) {
    return (operation: ToolOperation) => {
      closeDialog();
      setActiveOperation({ id: operation.id, name: tool.name });
    };
  }

  function recover(slug: string, action: "retry" | "rollback") {
    if (!authorize(update)) return;
    const operation = statusMap.get(slug)?.operation;
    const tool = tools.find((item) => item.slug === slug);
    if (!operation || !tool) return;
    recovery.mutate(
      { slug, cluster_id: clusterId, operationId: operation.id, action },
      { onSuccess: trackOperation(tool) },
    );
  }

  function confirmInstall(valuesOverride: string | undefined, preset: string) {
    if (dialog?.kind !== "install" || !authorize(create)) return;
    install.mutate(
      {
        slug: dialog.tool.slug,
        cluster_id: clusterId,
        preset,
        values_override: valuesOverride,
      },
      { onSuccess: trackOperation(dialog.tool) },
    );
  }

  function confirmRemoval() {
    if (dialog?.kind !== "uninstall" || !authorize(remove)) return;
    uninstall.mutate(
      { slug: dialog.tool.slug, cluster_id: clusterId },
      { onSuccess: trackOperation(dialog.tool) },
    );
  }

  function adoptRelease(slug: string, releaseName: string) {
    if (!authorize(create)) return;
    adopt.mutate({ slug, cluster_id: clusterId, release_name: releaseName });
  }

  const installDialog: ComponentProps<typeof ToolInstallModal> | null =
    dialog?.kind === "install"
      ? {
          tool: dialog.tool,
          clusterId,
          preset: defaultPreset,
          onConfirm: confirmInstall,
          onClose: closeDialog,
          installing: install.isPending,
          confirmDecision: create,
        }
      : null;
  const confirmation: ComponentProps<typeof ConfirmDialog> | null =
    dialog && dialog.kind !== "install"
      ? {
          open: true,
          onClose: closeDialog,
          variant: "destructive",
          ...(dialog.kind === "rollback"
            ? {
                title: `Roll back ${dialog.tool.name}`,
                description:
                  "Releases are reverted in reverse installation order. Newly installed releases are removed; upgraded releases return to their previous revisions. Existing releases that were only adopted are preserved.",
                confirmText: "Roll back",
                onConfirm: () => recover(dialog.tool.slug, "rollback"),
                loading: recovery.isPending,
                confirmDisabledReason: disabledReason(update),
              }
            : {
                title: "Disable Tool",
                description: `This will uninstall ${dialog.tool.name} from the cluster. All related resources will be removed.`,
                confirmText: "Disable",
                onConfirm: confirmRemoval,
                loading: uninstall.isPending,
                confirmDisabledReason: disabledReason(remove),
              }),
        }
      : null;
  const progress: ComponentProps<typeof ToolInstallProgress> | null =
    activeOperation
      ? {
          operationId: activeOperation.id,
          toolName: activeOperation.name,
          onClose: () => setActiveOperation(null),
        }
      : null;

  function cardProps(
    tool: ClusterTool,
  ): Omit<ComponentProps<typeof ToolCard>, "clusterDisconnected"> {
    return {
      tool,
      toolStatus: statusMap.get(tool.slug),
      onInstall: (slug) => openDialog("install", slug),
      onUninstall: (slug) => openDialog("uninstall", slug),
      onAdopt: adoptRelease,
      onRecover: (slug, action) =>
        action === "rollback"
          ? openDialog("rollback", slug)
          : recover(slug, action),
      installDisabledReason: disabledReason(create),
      adoptDisabledReason: disabledReason(create),
      uninstallDisabledReason: disabledReason(remove),
      recoveryDisabledReason: disabledReason(update),
      installing: install.isPending && install.variables?.slug === tool.slug,
      uninstalling:
        uninstall.isPending && uninstall.variables?.slug === tool.slug,
    };
  }

  return { cardProps, installDialog, confirmation, progress };
}
