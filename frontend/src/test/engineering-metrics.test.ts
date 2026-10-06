import { afterEach, expect, it, vi } from "vitest";
import {
  classify,
  completeSearchFixture,
  InteractionSamples,
  MAX_ACTIVE,
  MAX_SAMPLES,
  WindowCounters,
  provenance,
} from "../../tests/benchmark/engineering-metrics";
import {
  installEngineeringBrowser,
  sanitizeBrowserSnapshot,
} from "../../tests/benchmark/engineering-browser";
afterEach(() => {
  (
    window as unknown as { __astronomerEngineering?: { stop(): unknown } }
  ).__astronomerEngineering?.stop();
  sessionStorage.removeItem("astronomer:engineering-window:v1");
  delete (window as unknown as { __astronomerEngineering?: unknown })
    .__astronomerEngineering;
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
it("classifies only fixed groups and never keeps identities or query strings", () => {
  const origin = "https://example.test";
  expect(
    classify(
      origin + "/api/v1/delivery/clusters/SECRET/inventory/?token=SECRET",
      origin,
    ),
  ).toBe("inventory");
  expect(classify("https://SECRET.test/api/v1/clusters/", origin)).toBe(
    "other",
  );
  expect(classify("invalid SECRET", origin)).toBe("other");
  expect(
    classify(origin + "/api/v1/resources/search?name=SECRET", origin),
  ).toBe("search");
});
it("partitions completed, failed and unfinished streams at an immutable cutoff", () => {
  const counter = new WindowCounters(),
    complete = {},
    fail = {},
    stream = {};
  counter.start(complete, "clusters");
  counter.start(fail, "resources");
  counter.start(stream, "stream");
  counter.end(complete, false);
  counter.end(fail, true);
  counter.end({}, false);
  const result = counter.finish();
  expect(result.groups.clusters).toMatchObject({
    started: 1,
    completed: 1,
    failed: 0,
    unfinished: 0,
  });
  expect(result.groups.resources.failed).toBe(1);
  expect(result.groups.stream.unfinished).toBe(1);
  expect(result.carry_in_completions).toBe(1);
  counter.end(stream, false);
  counter.start({}, "clusters");
  expect(counter.finish()).toEqual(result);
});
it("bounds retained active requests and excludes overflow from carry-in completions", () => {
  const counter = new WindowCounters();
  for (let index = 0; index < MAX_ACTIVE; index++)
    counter.start({}, "resources");
  const extra = {};
  counter.start(extra, "resources");
  counter.end(extra, false);
  expect(counter.finish().dropped_requests).toBe(1);
  expect(counter.finish().carry_in_completions).toBe(0);
});
it("calculates nearest-rank percentiles and reports failed/censored samples", () => {
  const samples = new InteractionSamples();
  for (let index = 1; index <= 100; index++) samples.add(index, true);
  samples.add(1000, false);
  expect(samples.snapshot()).toMatchObject({
    attempted: 101,
    completed: 100,
    failed: 1,
    p50_ms: 50,
    p95_ms: 95,
  });
  for (let index = 100; index <= MAX_SAMPLES; index++) samples.add(index, true);
  expect(samples.snapshot()).toMatchObject({
    dropped_samples: 1,
    p95_ms: null,
    max_ms: null,
  });
});
function browserFixture() {
  const observers: {
    callback: (list: { getEntries(): PerformanceEntry[] }) => void;
    disconnect: ReturnType<typeof vi.fn>;
    takeRecords: ReturnType<typeof vi.fn>;
  }[] = [];
  class Observer {
    static supportedEntryTypes = ["resource", "navigation", "longtask"];
    disconnect = vi.fn();
    takeRecords = vi.fn(() => []);
    observe = vi.fn();
    constructor(
      readonly callback: (list: { getEntries(): PerformanceEntry[] }) => void,
    ) {
      observers.push(this);
    }
  }
  vi.stubGlobal("PerformanceObserver", Observer);
  const start = performance.timeOrigin + 100;
  vi.setSystemTime(start);
  installEngineeringBrowser({ start, end: start + 1000, id: 1 });
  const controller = (
    window as unknown as {
      __astronomerEngineering: { stop(cutoff?: number): unknown };
    }
  ).__astronomerEngineering;
  const entry = (
    time: number,
    duration: number,
    bytes = [300, 200, 400],
    name = location.origin + "/SECRET?token=SECRET",
  ) =>
    ({
      toJSON: () => ({}),
      entryType: "resource",
      name,
      startTime: time,
      duration,
      transferSize: bytes[0],
      encodedBodySize: bytes[1],
      decodedBodySize: bytes[2],
    }) as PerformanceEntry;
  return { observers, controller, start, entry };
}
it("filters entries by actual cutoff, keeps cached zeros, and drops opaque sizes", () => {
  vi.useFakeTimers();
  const clear = vi.spyOn(window, "clearTimeout");
  const { observers, controller, start, entry } = browserFixture();
  observers[0].callback({
    getEntries: () => [
      entry(110, 10),
      entry(120, 10, [0, 0, 0]),
      entry(130, 10, [1, 1, 1], "https://SECRET.test/private"),
      entry(700, 20),
      entry(50, 60),
    ],
  });
  const snapshot = controller.stop(start + 500);
  expect(snapshot).toMatchObject({
    completed_entries: 2,
    transfer_bytes: 300,
    encoded_body_bytes: 200,
    decoded_body_bytes: 400,
    opaque_entries: 1,
    excluded_boundary_entries: 2,
  });
  expect(JSON.stringify(snapshot)).not.toContain("SECRET");
  expect(
    observers.every((observer) => observer.disconnect.mock.calls.length === 1),
  ).toBe(true);
  expect(clear).toHaveBeenCalled();
  clear.mockRestore();
});
it("caps numeric resource retention and sanitizes unexpected page fields", () => {
  vi.useFakeTimers();
  const { observers, controller, entry } = browserFixture();
  observers[0].callback({
    getEntries: () => Array.from({ length: 4100 }, () => entry(110, 10)),
  });
  const raw = controller.stop() as Record<string, unknown>;
  expect(raw.dropped_entries).toBe(4);
  const clean = sanitizeBrowserSnapshot({
    ...raw,
    url: "SECRET",
    headers: { token: "SECRET" },
    capabilities: { resource_timing: true, secret: "SECRET" },
  });
  expect(JSON.stringify(clean)).not.toContain("SECRET");
  expect(
    sanitizeBrowserSnapshot({ ...raw, transfer_bytes: NaN }),
  ).toBeUndefined();
});
it("reports unsupported observers explicitly and auto-cleans at deadline", () => {
  vi.useFakeTimers();
  vi.stubGlobal("PerformanceObserver", undefined);
  installEngineeringBrowser({
    start: Date.now(),
    end: Date.now() + 1000,
    id: 2,
  });
  vi.advanceTimersByTime(1000);
  const result = (
    window as unknown as { __astronomerEngineering: { stop(): unknown } }
  ).__astronomerEngineering.stop();
  expect(result).toMatchObject({
    capabilities: {
      resource_timing: false,
      long_tasks: false,
      react_commit_timing: false,
    },
  });
  expect(vi.getTimerCount()).toBe(0);
});

it("accepts only the complete immutable-hash provenance contract", () => {
  const declared = {
    source_commit: "a".repeat(40),
    images_sha256: "b".repeat(64),
    chart_values_sha256: "c".repeat(64),
    dataset_sha256: "d".repeat(64),
    hardware_sha256: "e".repeat(64),
  };
  expect(provenance(declared)).toMatchObject({
    status: "supplied_unverified",
    ...declared,
  });
  expect(provenance()).toMatchObject({ status: "missing" });
  expect(() => provenance({ ...declared, headers: "SECRET" })).toThrow(
    "invalid engineering provenance",
  );
  expect(() => provenance({ ...declared, dataset_sha256: "SECRET" })).toThrow(
    "invalid engineering provenance",
  );
});
it("navigation bootstrap reads latest numeric bounds and stops the previous observer set", () => {
  vi.useFakeTimers();
  const { observers, controller, start } = browserFixture();
  installEngineeringBrowser({ start, end: start + 900, id: 7 });
  expect(
    observers
      .slice(0, 3)
      .every((observer) => observer.disconnect.mock.calls.length === 1),
  ).toBe(true);
  installEngineeringBrowser();
  expect(
    (window as unknown as { __astronomerEngineering: { id: number } })
      .__astronomerEngineering.id,
  ).toBe(7);
  controller.stop();
  (
    window as unknown as { __astronomerEngineering: { stop(): unknown } }
  ).__astronomerEngineering.stop();
});

it("separates HTTP error responses from completed and transport-failed requests", () => {
  const counter = new WindowCounters(),
    failedHttp = {},
    stream = {};
  counter.start(failedHttp, "clusters");
  counter.response(failedHttp, 503);
  counter.end(failedHttp, false);
  counter.start(stream, "stream");
  counter.response(stream, 200);
  const result = counter.finish();
  expect(result.groups.clusters).toMatchObject({
    completed: 1,
    failed: 0,
    http_5xx: 1,
  });
  expect(result.groups.stream).toMatchObject({
    completed: 0,
    unfinished: 1,
    http_2xx: 1,
  });
  counter.response(stream, 500);
  expect(counter.finish()).toEqual(result);
});

it("requires complete search application results and the exact fixture scope", () => {
  const result = {
    clusters_failed: 0,
    truncated: false,
    errors: [],
    results: [{ cluster_id: "c", namespace: "n", name: "fixture" }],
  };
  expect(completeSearchFixture(result, "c", "n", "fixture")).toBe(true);
  for (const partial of [
    { clusters_failed: 1 },
    { truncated: true },
    { errors: [{}] },
    { results: [] },
  ])
    expect(
      completeSearchFixture({ ...result, ...partial }, "c", "n", "fixture"),
    ).toBe(false);
  expect(completeSearchFixture(result, "c", "other", "fixture")).toBe(false);
});
