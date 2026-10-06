import { lazy, Suspense, useState } from "react";
import { Copy, FormInput, Pencil } from "lucide-react";

import { hasGuidedEditForm } from "@/components/resources/resource-masthead-model";
import { Tooltip } from "@/components/ui/tooltip";
import { k8sGetYaml } from "@/lib/api/kubernetes-proxy";
import {
  cloneYamlFromSource,
  NON_CLONABLE_RESOURCE_TYPES,
} from "@/lib/k8s-clone";
import { useClusterResourcePermission } from "@/lib/permission-hooks";
import { toastApiError } from "@/lib/toast";

const CloneDialog = lazy(async () => ({
  default: (await import("./create-resource-dialog")).CreateResourceDialog,
}));

export type DetailEditMode = "guided" | "yaml";

const BTN =
  "inline-flex items-center gap-1.5 h-8 px-3 rounded-md border border-border bg-background text-sm font-medium text-foreground transition-colors hover:bg-accent disabled:opacity-50 disabled:cursor-not-allowed";

/**
 * Edit as form / Edit YAML / Clone for the detail masthead. Edit hands off to
 * the YAML tab's existing dry-run + apply flow and Clone to the existing
 * create dialog, so no new mutation path exists here; each control is gated on
 * the same RBAC decisions as the list row actions (the server stays the gate).
 */
export function ResourceDetailEditActions({
  clusterId,
  resourceType,
  kind,
  name,
  k8sPath,
  permissionResource,
  updateAllowed,
  updateReason,
  onEdit,
}: {
  clusterId: string;
  resourceType: string;
  kind?: string;
  name: string;
  k8sPath: string;
  permissionResource?: string;
  updateAllowed: boolean;
  updateReason?: string;
  onEdit: (mode: DetailEditMode) => void;
}) {
  const read = useClusterResourcePermission(
    clusterId,
    resourceType,
    "read",
    permissionResource,
  );
  const create = useClusterResourcePermission(
    clusterId,
    resourceType,
    "create",
    permissionResource,
  );
  const [cloneYaml, setCloneYaml] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const cloneDenied = !read.allowed
    ? read.disabledReason || read.reason
    : !create.allowed
      ? create.disabledReason || create.reason
      : NON_CLONABLE_RESOURCE_TYPES.has(resourceType)
        ? "Infrastructure and controller-owned resources require an explicit manifest, not cloning."
        : undefined;

  const clone = async () => {
    if (pending || cloneDenied) return;
    setPending(true);
    try {
      setCloneYaml(
        await cloneYamlFromSource(await k8sGetYaml(clusterId, k8sPath)),
      );
    } catch (error) {
      toastApiError("Failed to prepare clone", error);
    } finally {
      setPending(false);
    }
  };

  return (
    <>
      {hasGuidedEditForm(kind) && (
        <Tooltip content={updateAllowed ? undefined : updateReason} wrap>
          <button
            type="button"
            className={BTN}
            disabled={!updateAllowed}
            onClick={() => onEdit("guided")}
          >
            <FormInput className="h-3.5 w-3.5" /> Edit as form
          </button>
        </Tooltip>
      )}
      <Tooltip content={updateAllowed ? undefined : updateReason} wrap>
        <button
          type="button"
          className={BTN}
          disabled={!updateAllowed}
          onClick={() => onEdit("yaml")}
        >
          <Pencil className="h-3.5 w-3.5" /> Edit YAML
        </button>
      </Tooltip>
      <Tooltip content={cloneDenied} wrap>
        <button
          type="button"
          className={BTN}
          disabled={!!cloneDenied || pending}
          onClick={() => void clone()}
        >
          <Copy className="h-3.5 w-3.5" /> Clone
        </button>
      </Tooltip>
      {cloneYaml !== null && (
        <Suspense fallback={<span role="status">Loading clone editor…</span>}>
          <CloneDialog
            open
            clusterId={clusterId}
            title={`Clone ${name}`}
            initialYaml={cloneYaml}
            onClose={() => setCloneYaml(null)}
          />
        </Suspense>
      )}
    </>
  );
}
