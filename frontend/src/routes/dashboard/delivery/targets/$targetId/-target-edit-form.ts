import type { updateDeliveryTarget } from "@/lib/api/delivery-targets";
import type { DriftPolicy } from "@/lib/api/delivery-bundles";
import {
  placementFromForm,
  placementHasSelector,
} from "@/components/delivery/target-form";
import { targetOverridesFromForm } from "@/components/delivery/target-overrides-editor";

type UpdateBody = Parameters<typeof updateDeliveryTarget>[1];

/**
 * Parses the target-edit form into the PATCH body, or throws a
 * user-facing Error when the placement/maintenance fields are invalid.
 * Extracted from TargetEditDialog so the dialog component stays under
 * the function-length budget.
 */
export function buildTargetUpdatePayload(
  form: FormData,
  allClusters: boolean,
  drift: DriftPolicy,
  projectId: string,
): UpdateBody {
  const placement = placementFromForm(form, allClusters);
  if (!placementHasSelector(placement)) {
    throw new Error(
      "Select at least one explicit cluster, group, label, or expression. Empty placement selects nothing.",
    );
  }
  const maintenanceText = String(form.get("maintenance") ?? "").trim();
  const maintenanceWindowPolicy = maintenanceText
    ? (JSON.parse(maintenanceText) as Record<string, unknown>)
    : {};
  if (
    maintenanceWindowPolicy === null ||
    Array.isArray(maintenanceWindowPolicy) ||
    typeof maintenanceWindowPolicy !== "object"
  ) {
    throw new Error("Maintenance policy must be a JSON object.");
  }
  return {
    project_id: projectId,
    description: String(form.get("description") ?? "").trim() || undefined,
    bundle_version_id: String(form.get("bundle_version_id") ?? "").trim(),
    placement,
    rollout_policy: {
      approval_required: form.get("approval_required") === "on",
    },
    reconciliation_policy: {
      interval: String(form.get("interval")),
      retry_interval: String(form.get("retry_interval")),
      timeout: String(form.get("timeout")),
      prune: form.get("prune") === "on",
      wait: form.get("wait") === "on",
      drift,
    },
    maintenance_window_policy: maintenanceWindowPolicy,
    overrides: targetOverridesFromForm(form),
  };
}
