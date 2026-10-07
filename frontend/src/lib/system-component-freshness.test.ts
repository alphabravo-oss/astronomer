import {
  componentFreshness,
  componentHealthSummary,
} from "./system-component-freshness";
const now = Date.parse("2026-10-06T12:00:00Z");
const observedAt = new Date(now).toISOString();
const healthy = { health: "healthy" as const };
describe("component source freshness", () => {
  it.each([
    "stale",
    "unsynced",
    "denied",
    "absent",
    "disconnected",
    "unavailable",
  ] as const)("%s overrides cached healthy data", (state) => {
    expect(
      componentFreshness(
        { ...healthy, observation: { state, observedAt } },
        now,
      ),
    ).toMatchObject({ state, health: state, attention: true });
  });
  it("ages current data without mutating the cached payload", () => {
    const component = Object.freeze({
      ...healthy,
      observation: Object.freeze({ state: "current" as const, observedAt }),
    });
    expect(componentFreshness(component, now + 300_000).health).toBe("healthy");
    expect(componentFreshness(component, now + 300_001).health).toBe("stale");
    expect(component.observation.state).toBe("current");
  });
  it.each([undefined, "nonsense", "2026-10-06T12:00:31Z"])(
    "rejects current time %s",
    (time) => {
      expect(
        componentFreshness(
          { ...healthy, observation: { state: "current", observedAt: time } },
          now,
        ),
      ).toEqual({
        state: "unavailable",
        health: "unavailable",
        attention: true,
        observedAt: undefined,
      });
    },
  );
  it("allows bounded skew and preserves current unhealthy reconciliation", () => {
    expect(
      componentFreshness(
        {
          health: "degraded",
          observation: { state: "current", observedAt: "2026-10-06T12:00:30Z" },
        },
        now,
      ),
    ).toMatchObject({ state: "current", health: "degraded", attention: true });
  });
  it("preserves never-observed states without fabricating timestamps", () => {
    expect(
      componentFreshness({ ...healthy, observation: { state: "denied" } }, now),
    ).toEqual({
      state: "denied",
      health: "denied",
      attention: true,
      observedAt: undefined,
    });
  });
  it("preserves legacy health without inventing source age", () => {
    expect(componentFreshness(healthy, now + 900_000)).toEqual({
      state: "legacy",
      health: "healthy",
      attention: false,
    });
    expect(componentFreshness({ health: "unknown" }, now).attention).toBe(
      false,
    );
    expect(componentFreshness({ health: "degraded" }, now).attention).toBe(
      true,
    );
  });
  it("counts the same effective health shown by badges", () => {
    const components = [
      healthy,
      { ...healthy, observation: { state: "current" as const, observedAt } },
      { ...healthy, observation: { state: "denied" as const } },
    ];
    expect(componentHealthSummary(components, now)).toEqual({
      healthy: 2,
      attention: 1,
    });
    expect(componentHealthSummary(components, now + 300_001)).toEqual({
      healthy: 1,
      attention: 2,
    });
  });
});
