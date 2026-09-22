import type {
  AmountType,
  RolloutFailureAction,
  RolloutStrategyRequest,
  RolloutStrategyType,
} from "@/lib/api/delivery-rollouts";
import type { PlacementPreview } from "@/lib/api/delivery-targets";

/**
 * Parses the launch-rollout form into a RolloutStrategyRequest, or throws a
 * user-facing Error for the all-cluster confirmation / partitions cases.
 * Extracted from LaunchDialog so the dialog component stays under the
 * function-length budget.
 */
export function buildRolloutStrategyPayload(
  form: FormData,
  state: {
    strategyType: RolloutStrategyType;
    maxUnavailableType: AmountType;
    failureType: AmountType;
    onFailure: RolloutFailureAction;
  },
  preview: PlacementPreview,
): RolloutStrategyRequest {
  if (preview.requiresAllConfirmation && form.get("confirm_all") !== "on") {
    throw new Error("Explicit all-cluster confirmation is required.");
  }
  const partitionsText = String(form.get("partitions") ?? "").trim();
  const partitions = partitionsText
    ? (JSON.parse(partitionsText) as RolloutStrategyRequest["partitions"])
    : undefined;
  const explicitCanaries = String(form.get("canary_ids") ?? "")
    .split(",")
    .map((id) => id.trim())
    .filter(Boolean);
  const { strategyType, maxUnavailableType, failureType, onFailure } = state;
  return {
    type: strategyType,
    max_concurrent: Number(form.get("max_concurrent")),
    max_unavailable: {
      type: maxUnavailableType,
      value: Number(form.get("max_unavailable")),
    },
    min_ready: String(form.get("min_ready")),
    progress_deadline: String(form.get("deadline")),
    failure_threshold: {
      type: failureType,
      value: Number(form.get("failure_threshold")),
    },
    on_failure: onFailure,
    respect_maintenance_windows: form.get("respect_windows") === "on",
    shuffle_seed: String(form.get("shuffle_seed") ?? "").trim() || undefined,
    ...(strategyType === "canary"
      ? {
          canary: explicitCanaries.length
            ? {
                size: { type: "count" as const, value: 0 },
                cluster_ids: explicitCanaries,
                approval_after_canary:
                  form.get("approval_after_canary") === "on",
                soak: String(form.get("canary_soak")),
              }
            : {
                size: {
                  type: String(form.get("canary_size_type")) as AmountType,
                  value: Number(form.get("canary_size")),
                },
                approval_after_canary:
                  form.get("approval_after_canary") === "on",
                soak: String(form.get("canary_soak")),
              },
        }
      : {}),
    ...(strategyType === "partitioned" ? { partitions } : {}),
  };
}
