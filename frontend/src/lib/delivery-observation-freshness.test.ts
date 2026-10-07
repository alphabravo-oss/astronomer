import {
  deliveryObservationFreshness,
  type DeliveryObservation,
} from "./delivery-observation-freshness";
const now = Date.parse("2026-10-06T12:00:00Z");
const observedAt = new Date(now).toISOString();
it.each([
  "current",
  "stale",
  "unsynced",
  "denied",
  "absent",
  "disconnected",
  "unavailable",
] as const)("preserves explicit %s state", (state) => {
  expect(deliveryObservationFreshness({ state, observedAt }, now)).toEqual({
    state,
    observedAt,
  });
});
it("expires current state without changing its immutable input", () => {
  const observation = Object.freeze({ state: "current" as const, observedAt });
  expect(deliveryObservationFreshness(observation, now + 300_000).state).toBe(
    "current",
  );
  expect(deliveryObservationFreshness(observation, now + 300_001).state).toBe(
    "stale",
  );
  expect(observation.state).toBe("current");
});
it.each([undefined, null, "invalid", "2026-10-06T12:00:31Z"])(
  "rejects invalid current timestamp %s",
  (time) => {
    const observation = {
      state: "current",
      observedAt: time,
    } as DeliveryObservation;
    expect(deliveryObservationFreshness(observation, now)).toEqual({
      state: "unavailable",
      observedAt: undefined,
    });
  },
);
it("allows bounded future skew", () => {
  expect(
    deliveryObservationFreshness(
      { state: "current", observedAt: "2026-10-06T12:00:30Z" },
      now,
    ).state,
  ).toBe("current");
});
it("does not invent source time for missing or never-observed metadata", () => {
  expect(deliveryObservationFreshness(undefined, now)).toEqual({
    state: "unknown",
  });
  expect(deliveryObservationFreshness({ state: "denied" }, now)).toEqual({
    state: "denied",
    observedAt: undefined,
  });
});
it("rejects unknown wire state", () => {
  expect(
    deliveryObservationFreshness(
      { state: "future-state" } as unknown as DeliveryObservation,
      now,
    ).state,
  ).toBe("unavailable");
});
