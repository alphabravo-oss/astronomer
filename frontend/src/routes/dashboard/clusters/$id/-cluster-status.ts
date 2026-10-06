import type { Cluster } from "@/types";
import { formatPercentage, formatRelativeTime } from "@/lib/utils";

export interface EffectiveClusterStatus {
  /** Status word understood by `StatusBadge`. */
  status: string;
  label: string;
}

const LIFECYCLE_LABELS: Record<string, string> = {
  created: "Created, waiting for registration",
  awaiting_agent: "Waiting for agent",
  connected: "Agent connected",
  provisioning: "Applying baseline",
  failed: "Registration failed",
};

/**
 * Collapse connection status, lifecycle (decommissioning) and registration
 * failure into the one status an operator should see in the masthead.
 */
export function deriveEffectiveClusterStatus(
  cluster: Pick<Cluster, "status" | "decommissioning" | "registrationPhase">,
): EffectiveClusterStatus {
  if (cluster.decommissioning) {
    return { status: "decommissioning", label: "Decommissioning" };
  }
  switch (cluster.status) {
    case "active":
      return { status: "active", label: "Active" };
    case "disconnected":
      return { status: "disconnected", label: "Disconnected" };
    case "error":
      return { status: "error", label: "Error" };
    case "pending":
      return cluster.registrationPhase === "failed"
        ? { status: "error", label: "Registration failed" }
        : { status: "pending", label: "Pending" };
    default:
      return { status: "unknown", label: "Unknown" };
  }
}

/** Lifecycle detail for the metadata line; hidden once adoption is complete. */
export function clusterLifecycleDetail(
  cluster: Pick<Cluster, "registrationPhase">,
): string | undefined {
  const phase = cluster.registrationPhase;
  return phase && phase !== "ready" ? LIFECYCLE_LABELS[phase] : undefined;
}

/**
 * A usage card either shows a percentage with where it came from, or "No data"
 * with no number; never a number captioned "No data".
 */
export function usageCardView(input: {
  percentage: number | null;
  usage: number | null;
  capacity: number | null;
  format: (value: number) => string;
  sampledAt?: string | null;
}): { value: string; subtitle: string; percentage?: number } {
  const { percentage, usage, capacity, format, sampledAt } = input;
  if (percentage == null) return { value: "—", subtitle: "No data" };
  const subtitle =
    usage != null && capacity != null
      ? `${format(usage)} / ${format(capacity)}`
      : sampledAt
        ? `as of ${formatRelativeTime(sampledAt)}`
        : "Latest sample";
  return { value: formatPercentage(percentage), subtitle, percentage };
}
