import {
  assertBenchmarkMetrics,
  BENCHMARK_SCHEMA_VERSION,
  type BrowserInteractionMetrics,
} from "../../tests/benchmark/instrumentation";

const valid: BrowserInteractionMetrics = {
  active_ms: 1250,
  wall_ms: 1400,
  pointer_activations: 4,
  keyboard_activations: 1,
  route_transitions: 2,
  error_count: 1,
  recovery_actions: 1,
  duplicate_effects: 0,
};

describe("benchmark-v1 instrumentation contract", () => {
  it("uses the retained evidence schema version", () => {
    expect(BENCHMARK_SCHEMA_VERSION).toBe("rancher-ux-benchmark/v1");
  });

  it("accepts only non-negative integer measurements", () => {
    expect(() => assertBenchmarkMetrics(valid)).not.toThrow();
    expect(() =>
      assertBenchmarkMetrics({ ...valid, pointer_activations: -1 }),
    ).toThrow(/pointer_activations/);
    expect(() =>
      assertBenchmarkMetrics({ ...valid, wall_ms: Number.NaN }),
    ).toThrow(/wall_ms/);
  });
});
