import type { DeliverySystemComponent } from "@/lib/api/delivery-system";

const MAX_AGE_MS = 5 * 60_000;
const MAX_FUTURE_SKEW_MS = 30_000;
type Component = Pick<DeliverySystemComponent, "health" | "observation">;
type ObservationState = NonNullable<Component["observation"]>["state"];

export interface ComponentFreshness {
  state: ObservationState | "legacy";
  health: string;
  attention: boolean;
  // Only trusted source times are displayed. Receipt time is never substituted.
  observedAt?: string;
}

/** One presentation contract for badges, filters, counts, and nested evidence. */
export function componentFreshness(
  component: Component,
  now: number,
): ComponentFreshness {
  const observation = component.observation;
  if (!observation) {
    return {
      state: "legacy",
      health: component.health,
      attention: ["degraded", "unavailable"].includes(component.health),
    };
  }
  const timestamp = observation.observedAt
    ? Date.parse(observation.observedAt)
    : NaN;
  const validTime =
    Number.isFinite(timestamp) && timestamp <= now + MAX_FUTURE_SKEW_MS;
  let state = observation.state;
  if (
    ![
      "current",
      "stale",
      "unsynced",
      "denied",
      "absent",
      "disconnected",
      "unavailable",
    ].includes(state)
  ) {
    state = "unavailable";
  }
  if (
    (observation.observedAt && !validTime) ||
    (["current", "stale", "absent"].includes(state) && !validTime)
  ) {
    state = "unavailable";
  } else if (state === "current" && now - timestamp > MAX_AGE_MS) {
    state = "stale";
  }
  const health = state === "current" ? component.health : state;
  return {
    state,
    health,
    attention:
      state !== "current" || ["degraded", "unavailable"].includes(health),
    observedAt: validTime ? observation.observedAt : undefined,
  };
}

export function componentHealthSummary(
  components: readonly Component[],
  now: number,
) {
  return components.reduce(
    (counts, component) => {
      const presentation = componentFreshness(component, now);
      if (presentation.health === "healthy") counts.healthy++;
      if (presentation.attention) counts.attention++;
      return counts;
    },
    { healthy: 0, attention: 0 },
  );
}

export function evidenceHealth(
  freshness: ComponentFreshness,
  reported: string | undefined,
): string {
  return freshness.state === "current" || freshness.state === "legacy"
    ? reported || "unknown"
    : freshness.state;
}
