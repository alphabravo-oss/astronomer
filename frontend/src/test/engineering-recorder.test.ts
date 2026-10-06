import { EventEmitter } from "node:events";
import type { Page } from "@playwright/test";
import { afterEach, expect, it, vi } from "vitest";
import { EngineeringRecorder } from "../../tests/benchmark/engineering-recorder";
import { installEngineeringBrowser } from "../../tests/benchmark/engineering-browser";
afterEach(() => {
  vi.useRealTimers();
});
function fixture() {
  const emitter = new EventEmitter();
  const session = {
    detach: vi.fn(async () => {}),
    send: vi.fn(async () => ({
      metrics: [
        { name: "JSHeapUsedSize", value: 100 },
        { name: "LayoutDuration", value: 2 },
        { name: "SECRET", value: 999 },
      ],
    })),
  };
  const context = { newCDPSession: vi.fn(async () => session) };
  const frame = {};
  const page = Object.assign(emitter, {
    addInitScript: vi.fn(async () => {}),
    context: () => context,
    mainFrame: () => frame,
    evaluate: vi.fn(async (fn: unknown) =>
      fn === installEngineeringBrowser ? undefined : undefined,
    ),
  });
  return {
    page: page as unknown as Page,
    emitter,
    raw: page,
    context,
    session,
    frame,
  };
}
function request() {
  return {
    url: () => "https://example.test/api/v1/clusters/?token=SECRET",
    headers: () => {
      throw new Error("must not inspect headers");
    },
    postData: () => {
      throw new Error("must not inspect body");
    },
  };
}
it("freezes network before collectors, cleans listeners and detaches CDP", async () => {
  vi.useFakeTimers();
  const { page, emitter, raw, session } = fixture();
  const recorder = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    1000,
  );
  const pending = request();
  emitter.emit("request", pending);
  let release!: (value: undefined) => void;
  raw.evaluate.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  const completion = recorder.finish();
  emitter.emit("requestfinished", pending);
  expect(emitter.listenerCount("request")).toBe(0);
  release(undefined);
  const artifact = await completion;
  expect(artifact.network.groups.clusters).toMatchObject({
    started: 1,
    completed: 0,
    unfinished: 1,
  });
  expect(artifact.cdp.final).toEqual({
    JSHeapUsedSize: 100,
    LayoutDuration: 2,
  });
  expect(JSON.stringify(artifact)).not.toContain("SECRET");
  expect(session.detach).toHaveBeenCalledOnce();
  expect(vi.getTimerCount()).toBe(0);
});
it("installs one navigation bootstrap across sequential windows and rejects concurrent ownership", async () => {
  vi.useFakeTimers();
  const { page, raw } = fixture();
  const first = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "stream_open",
    1000,
  );
  await expect(
    EngineeringRecorder.start(
      page,
      "https://example.test",
      "stream_fallback",
      1000,
    ),
  ).rejects.toThrow("active window");
  await first.finish();
  const second = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "stream_fallback",
    1000,
  );
  expect(raw.addInitScript).toHaveBeenCalledTimes(1);
  await first.finish();
  await expect(
    EngineeringRecorder.start(
      page,
      "https://example.test",
      "cluster_list",
      1000,
    ),
  ).rejects.toThrow("active window");
  await second.finish();
  expect(vi.getTimerCount()).toBe(0);
});
it("detaches a CDP session that resolves after its bounded acquisition timeout", async () => {
  vi.useFakeTimers();
  const { page, context, session } = fixture();
  let release!: (value: typeof session) => void;
  context.newCDPSession.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  const starting = EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    10_000,
  );
  await vi.advanceTimersByTimeAsync(1000);
  const recorder = await starting;
  release(session);
  await Promise.resolve();
  await Promise.resolve();
  expect(session.detach).toHaveBeenCalledOnce();
  expect((await recorder.finish()).cdp.status).toBe("missing");
});
it("stops browser instrumentation and releases page ownership after setup failure", async () => {
  vi.useFakeTimers();
  const { page, raw, emitter } = fixture();
  raw.evaluate.mockRejectedValueOnce(new Error("SECRET"));
  await expect(
    EngineeringRecorder.start(
      page,
      "https://example.test",
      "cluster_list",
      1000,
    ),
  ).rejects.toThrow("engineering recorder setup failed");
  expect(raw.evaluate).toHaveBeenCalledTimes(2);
  expect(emitter.listenerCount("request")).toBe(0);
  const retry = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    1000,
  );
  await retry.finish();
  expect(vi.getTimerCount()).toBe(0);
});
it("retains unfinished interactions at cutoff and invalidates duration deltas across navigation", async () => {
  vi.useFakeTimers();
  const { page, emitter, frame } = fixture();
  const recorder = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    1000,
  );
  let release!: () => void;
  const interaction = recorder.interaction(
    () =>
      new Promise<void>((resolve) => {
        release = resolve;
      }),
  );
  emitter.emit("framenavigated", frame);
  await vi.advanceTimersByTimeAsync(1000);
  const artifact = await recorder.finish();
  expect(artifact.interactions).toMatchObject({
    attempted: 1,
    completed: 0,
    unfinished: 1,
    p95_ms: null,
  });
  expect(artifact.cdp.delta_status).toBe("unavailable_navigation_reset");
  release();
  await interaction;
  expect(artifact.interactions.unfinished).toBe(1);
});
it("bounds a hung post-window browser collector", async () => {
  vi.useFakeTimers();
  const { page, raw, session } = fixture();
  const recorder = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "denied",
    10_000,
  );
  raw.evaluate.mockImplementation(() => new Promise(() => {}));
  const finishing = recorder.finish();
  await vi.advanceTimersByTimeAsync(3000);
  const artifact = await finishing;
  expect(artifact.browser_performance).toMatchObject({ status: "missing" });
  expect(session.detach).toHaveBeenCalledOnce();
  expect(vi.getTimerCount()).toBe(0);
});
it("records HTTP failure responses independently from transport completion", async () => {
  const { page, emitter } = fixture();
  const recorder = await EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    1000,
  );
  const pending = request();
  emitter.emit("request", pending);
  emitter.emit("response", { request: () => pending, status: () => 503 });
  emitter.emit("requestfinished", pending);
  const artifact = await recorder.finish();
  expect(artifact.network.groups.clusters).toMatchObject({
    completed: 1,
    failed: 0,
    http_5xx: 1,
  });
  expect(emitter.listenerCount("response")).toBe(0);
});
it("cleans a browser installation that completes after setup has timed out", async () => {
  vi.useFakeTimers();
  const { page, raw } = fixture();
  let release!: (value: undefined) => void;
  raw.evaluate.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        release = resolve;
      }),
  );
  const starting = EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    10000,
  );
  const rejected = expect(starting).rejects.toThrow(
    "engineering recorder setup failed",
  );
  await vi.advanceTimersByTimeAsync(1000);
  await rejected;
  expect(raw.evaluate).toHaveBeenCalledTimes(2);
  release(undefined);
  await vi.advanceTimersByTimeAsync(0);
  expect(raw.evaluate).toHaveBeenCalledTimes(3);
  expect(vi.getTimerCount()).toBe(0);
});

it("rejects setup that consumes the complete measurement window", async () => {
  vi.useFakeTimers();
  const { page, context, emitter } = fixture();
  context.newCDPSession.mockImplementationOnce(() => new Promise(() => {}));
  const starting = EngineeringRecorder.start(
    page,
    "https://example.test",
    "cluster_list",
    1000,
  );
  const rejected = expect(starting).rejects.toThrow(
    "engineering recorder setup failed",
  );
  await vi.advanceTimersByTimeAsync(1000);
  await rejected;
  expect(emitter.listenerCount("request")).toBe(0);
  expect(vi.getTimerCount()).toBe(0);
});
