import { lazy, Suspense, useState } from "react";
import { Copy, Download } from "lucide-react";
import { ActionMenu, type ActionMenuItem } from "@/components/ui/action-menu";
import { k8sGetYaml } from "@/lib/api/kubernetes-proxy";
import { k8sResourcePath } from "@/lib/k8s-paths";
import { prepareCloneManifest } from "@/lib/k8s-clone";
import {
  permissionDeniedReason,
  toastPermissionDenied,
} from "@/lib/permission-hooks";
import { toastApiError } from "@/lib/toast";
import { downloadBlob } from "@/lib/utils";
import type { ResourcePermissionDecisions } from "./resource-action-policy";

const CloneDialog = lazy(async () => ({
  default: (await import("./create-resource-dialog")).CreateResourceDialog,
}));
const NON_CLONABLE = new Set([
  "nodes",
  "events",
  "persistentvolumes",
  "customresourcedefinitions",
  "crds",
]);

/** One live-read/export/clone path for all explorer table families. */
export function ResourceActionMenu({
  clusterId,
  resourceType,
  row,
  permissions,
  items,
  k8sPath,
}: {
  clusterId: string;
  resourceType: string;
  row: { name: string; namespace?: string };
  permissions: ResourcePermissionDecisions;
  items: ActionMenuItem[];
  /** Explicit discovered CR path; built-in families use their canonical map. */
  k8sPath?: string;
}) {
  const [cloneYaml, setCloneYaml] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const cloneDenied = !permissions.read.allowed
    ? permissions.read
    : !permissions.create.allowed
      ? permissions.create
      : undefined;
  const safeKind = !NON_CLONABLE.has(resourceType);
  const transfer = async (clone: boolean) => {
    const denied = clone
      ? cloneDenied
      : !permissions.read.allowed
        ? permissions.read
        : undefined;
    if (denied) {
      toastPermissionDenied(denied);
      return;
    }
    if (pending || (clone && !safeKind)) return;
    setPending(true);
    try {
      const source = await k8sGetYaml(
        clusterId,
        k8sPath ?? k8sResourcePath(resourceType, row.name, row.namespace),
      );
      if (!clone) {
        downloadBlob(
          source,
          `${row.namespace ? row.namespace + "-" : ""}${row.name}.yaml`,
          "application/yaml",
        );
        return;
      }
      const yaml = await import("js-yaml");
      const parsed = yaml.load(source);
      if (
        !parsed ||
        typeof parsed !== "object" ||
        Array.isArray(parsed) ||
        !("apiVersion" in parsed) ||
        !("kind" in parsed)
      )
        throw new Error("The live response is not a Kubernetes object.");
      setCloneYaml(
        yaml.dump(prepareCloneManifest(parsed as Record<string, unknown>), {
          lineWidth: -1,
          noRefs: true,
        }),
      );
    } catch (error) {
      toastApiError(
        clone ? "Failed to prepare clone" : "Failed to download YAML",
        error,
      );
    } finally {
      setPending(false);
    }
  };
  return (
    <>
      <ActionMenu
        items={[
          ...items,
          {
            label: "Download YAML",
            icon: <Download className="h-3.5 w-3.5" />,
            onClick: () => void transfer(false),
            disabled: !permissions.read.allowed || pending,
            disabledReason: permissionDeniedReason(permissions.read),
            separator: true,
          },
          {
            label: "Clone",
            icon: <Copy className="h-3.5 w-3.5" />,
            onClick: () => void transfer(true),
            disabled: !!cloneDenied || pending || !safeKind,
            disabledReason: cloneDenied
              ? permissionDeniedReason(cloneDenied)
              : !safeKind
                ? "Infrastructure and controller-owned resources require an explicit manifest, not cloning."
                : undefined,
          },
        ]}
      />
      {cloneYaml !== null && (
        <Suspense fallback={<p role="status">Loading clone editor…</p>}>
          <CloneDialog
            open
            clusterId={clusterId}
            title={`Clone ${row.name}`}
            initialYaml={cloneYaml}
            onClose={() => setCloneYaml(null)}
          />
        </Suspense>
      )}
    </>
  );
}
