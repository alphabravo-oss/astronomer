import { describe, expect, it } from "vitest";
import {
  clusterLifecycleDetail,
  deriveEffectiveClusterStatus,
  usageCardView,
} from "./-cluster-status";

type Input = Parameters<typeof deriveEffectiveClusterStatus>[0];
const base: Input = {
  status: "active",
  decommissioning: false,
  registrationPhase: "ready",
};

describe("deriveEffectiveClusterStatus", () => {
  it("reports connected and active clusters as Active", () => {
    expect(deriveEffectiveClusterStatus(base)).toEqual({
      status: "active",
      label: "Active",
    });
  });

  it("reports a disconnected agent as Disconnected", () => {
    expect(
      deriveEffectiveClusterStatus({ ...base, status: "disconnected" }).label,
    ).toBe("Disconnected");
  });

  it("falls back to Unknown for an unrecognised connection status", () => {
    expect(
      deriveEffectiveClusterStatus({
        ...base,
        status: "bogus" as Input["status"],
      }),
    ).toEqual({ status: "unknown", label: "Unknown" });
  });

  it("lets decommissioning override connection status", () => {
    expect(
      deriveEffectiveClusterStatus({ ...base, decommissioning: true }),
    ).toEqual({ status: "decommissioning", label: "Decommissioning" });
  });

  it("surfaces a failed registration as an error and a normal one as Pending", () => {
    expect(
      deriveEffectiveClusterStatus({
        ...base,
        status: "pending",
        registrationPhase: "failed",
      }).status,
    ).toBe("error");
    expect(
      deriveEffectiveClusterStatus({
        ...base,
        status: "pending",
        registrationPhase: "awaiting_agent",
      }).label,
    ).toBe("Pending");
  });
});

describe("clusterLifecycleDetail", () => {
  it("is hidden when ready and described otherwise", () => {
    expect(clusterLifecycleDetail({ registrationPhase: "ready" })).toBe(
      undefined,
    );
    expect(clusterLifecycleDetail({ registrationPhase: "created" })).toMatch(
      /registration/,
    );
  });
});

describe("usageCardView", () => {
  const format = (n: number) => `${n}u`;
  it("shows No data with no number when there is no percentage", () => {
    expect(
      usageCardView({ percentage: null, usage: null, capacity: null, format }),
    ).toEqual({ value: "—", subtitle: "No data" });
  });
  it("shows usage over capacity when known", () => {
    expect(
      usageCardView({ percentage: 25, usage: 6, capacity: 24, format }),
    ).toMatchObject({ value: "25%", subtitle: "6u / 24u", percentage: 25 });
  });
  it("shows the sample time instead of No data when only a percentage exists", () => {
    const view = usageCardView({
      percentage: 25,
      usage: null,
      capacity: null,
      format,
      sampledAt: new Date().toISOString(),
    });
    expect(view.value).toBe("25%");
    expect(view.subtitle).toMatch(/^as of /);
  });
});
