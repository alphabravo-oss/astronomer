import type { OpenAPIComponents } from "@/types/openapi.generated";
import type { CamelizeKeys } from "@/types/wire-contract";
export type DeliveryObservation = CamelizeKeys<
  OpenAPIComponents["schemas"]["DeliveryObservation"]
>;
export interface ObservationFreshness {
  state: DeliveryObservation["state"] | "unknown";
  observedAt?: string;
}
const MAX_AGE_MS = 5 * 60_000;
const MAX_FUTURE_SKEW_MS = 30_000;
/** Source time only: callers must never substitute receipt or transition time. */
export function deliveryObservationFreshness(
  observation: DeliveryObservation | undefined,
  now: number,
): ObservationFreshness {
  if (!observation) return { state: "unknown" };
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
  return { state, observedAt: validTime ? observation.observedAt : undefined };
}
