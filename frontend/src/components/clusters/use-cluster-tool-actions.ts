import { useState, type ComponentProps } from "react";
import {
  useAdoptTool,
  useInstallTool,
  useRecoverTool,
  useToolConfiguration,
  useUninstallTool,
  useUpgradeTool,
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

type DialogKind = "install" | "upgrade" | "uninstall" | "rollback";
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
  const upgrade = useUpgradeTool();
  const uninstall = useUninstallTool();
  const adopt = useAdoptTool();
  const recovery = useRecoverTool();
  const [dialog, setDialog] = useState<ToolDialog | null>(null);
  const [activeOperation, setActiveOperation] = useState<{
    id: string;
    name: string;
  } | null>(null);
  const configuration = useToolConfiguration(
    dialog?.kind === "upgrade" ? dialog.tool.slug : "",
    clusterId,
  );
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
    if (!dialog || !["install", "upgrade"].includes(dialog.kind)) return;
    const mutation = dialog.kind === "install" ? install : upgrade;
    const decision = dialog.kind === "install" ? create : update;
    if (!authorize(decision)) return;
    mutation.mutate(
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
    const failedRelease = statusMap.get(dialog.tool.slug)?.status === "failed";
    uninstall.mutate(
      {
        slug: dialog.tool.slug,
        cluster_id: clusterId,
        ...(dialog.tool.slug === "longhorn"
          ? { confirm_data_deletion: true }
          : {}),
        ...(failedRelease ? { confirm_failed_release_cleanup: true } : {}),
      },
      { onSuccess: trackOperation(dialog.tool) },
    );
  }

  function adoptRelease(slug: string, releaseName: string) {
    if (!authorize(create)) return;
    adopt.mutate({ slug, cluster_id: clusterId, release_name: releaseName });
  }

  const installDialog: ComponentProps<typeof ToolInstallModal> | null =
    dialog && ["install", "upgrade"].includes(dialog.kind)
      ? {
          tool: dialog.tool,
          clusterId,
          preset: defaultPreset,
          action: dialog.kind as "install" | "upgrade",
          ...(dialog.kind === "upgrade"
            ? {
                initialValuesYaml: configuration.data?.valuesYaml,
                initialPreset: configuration.data?.preset,
                loadingInitialValues: configuration.isLoading,
                initialValuesError: configuration.error,
              }
            : {}),
          onConfirm: confirmInstall,
          onClose: closeDialog,
          installing:
            dialog.kind === "install" ? install.isPending : upgrade.isPending,
          confirmDecision: dialog.kind === "install" ? create : update,
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
                title: `Uninstall ${dialog.tool.name}`,
                description:
                  statusMap.get(dialog.tool.slug)?.status === "failed"
                    ? `Astronomer will verify and remove the incomplete ${dialog.tool.name} release created by the failed operation.`
                    : `Astronomer will uninstall each managed ${dialog.tool.name} release in reverse installation order.`,
                confirmText: "Uninstall",
                confirmValue: dialog.tool.name,
                impact: {
                  scope: `${clusterId} / ${dialog.tool.name}`,
                  consequences:
                    dialog.tool.slug === "longhorn"
                      ? [
                          "Astronomer will enable Longhorn's deletion-confirmation setting before uninstalling the release.",
                          "Longhorn volumes and their stored data may be permanently deleted by the chart's uninstall job.",
                        ]
                      : [
                          "Managed Helm releases and their chart-owned resources will be removed.",
                          "Persistent data and custom resources follow the chart's own deletion policy.",
                        ],
                  recovery:
                    "The operation remains visible with per-release progress and errors. Reinstalling does not guarantee recovery of deleted data.",
                },
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
      onUpgrade: (slug) => openDialog("upgrade", slug),
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
