import { isDexRuntimeApplied } from "./hooks";

describe("Dex phased mutation outcomes", () => {
  test("prepare staging is not reported as applied success", () => {
    expect(isDexRuntimeApplied({ applied: false })).toBe(false);
    expect(isDexRuntimeApplied({ applied: false, verified: false })).toBe(
      false,
    );
  });

  test("registration is verified only after applied and verified", () => {
    expect(isDexRuntimeApplied({ applied: true, verified: false })).toBe(false);
    expect(isDexRuntimeApplied({ applied: true, verified: true })).toBe(true);
    expect(isDexRuntimeApplied({ applied: true })).toBe(true);
  });
});
