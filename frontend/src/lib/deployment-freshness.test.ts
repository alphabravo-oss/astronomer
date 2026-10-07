import {
  deploymentFreshness,
  deploymentDrift,
  reportedConditionStatus,
} from "./deployment-freshness";
const now = Date.parse("2026-10-06T12:00:00Z");
const observedAt = new Date(now).toISOString();
const inventory = { entries: 0, ready: 0, failed: 0 };
it.each(["ready", "failed", "degraded"] as const)(
  "preserves current %s phase",
  (phase) => {
    expect(
      deploymentFreshness(
        {
          phase,
          inventory: {
            ...inventory,
            observation: { state: "current", observedAt },
          },
        },
        now,
      ).phase,
    ).toBe(phase);
  },
);
it.each([
  "stale",
  "unsynced",
  "denied",
  "absent",
  "disconnected",
  "unavailable",
] as const)("overrides cached ready phase with %s", (state) => {
  expect(
    deploymentFreshness(
      {
        phase: "ready",
        inventory: { ...inventory, observation: { state, observedAt } },
      },
      now,
    ).phase,
  ).toBe(state);
});
it.each(["ready", "failed", "deleting", "removed"] as const)(
  "preserves reported %s phase without asserting source freshness",
  (phase) => {
    expect(deploymentFreshness({ phase, inventory }, now)).toEqual({
      state: "unknown",
      phase,
    });
  },
);
it("ignores recent receipt time when source time is missing or expired", () => {
  const deployment = {
    phase: "ready" as const,
    inventory: { ...inventory, observation: { state: "current" as const } },
    lastObservedAt: observedAt,
  };
  expect(deploymentFreshness(deployment, now).phase).toBe("unavailable");
  expect(
    deploymentFreshness(
      {
        ...deployment,
        inventory: {
          ...inventory,
          observation: { state: "current", observedAt },
        },
      },
      now + 300_001,
    ).phase,
  ).toBe("stale");
});
it("cannot claim no drift from stale or unknown source evidence", () => {
  expect(deploymentDrift({ state: "stale" }, [])).toBe("Drift evidence stale");
  expect(deploymentDrift({ state: "unknown" }, [])).toBe(
    "Source freshness unknown",
  );
  expect(deploymentDrift({ state: "current" }, [])).toBe("No drift reported");
});
it("preserves current condition semantics", () => {
  expect(reportedConditionStatus({ type: "Ready", status: "True" })).toBe(
    "ready",
  );
  expect(reportedConditionStatus({ type: "Ready", status: "False" })).toBe(
    "false",
  );
});
