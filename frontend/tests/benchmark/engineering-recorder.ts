import type { CDPSession, Page, Request, Response } from "@playwright/test";
import {
  bounded,
  classify,
  InteractionSamples,
  journeys,
  type Journey,
  WindowCounters,
  provenance,
} from "./engineering-metrics";
import {
  installEngineeringBrowser,
  sanitizeBrowserSnapshot,
} from "./engineering-browser";
const metricNames = [
  "JSHeapUsedSize",
  "JSHeapTotalSize",
  "LayoutDuration",
  "RecalcStyleDuration",
  "TaskDuration",
] as const;
const installed = new WeakMap<Page, Promise<void>>();
const active = new WeakMap<Page, number>();
let nextWindowID = 0;
type Memory = Partial<Record<(typeof metricNames)[number], number>>;
export class EngineeringRecorder {
  private readonly windowID = ++nextWindowID;
  private readonly network = new WindowCounters();
  private readonly interactions = new InteractionSamples();
  private readonly started = performance.now();
  private readonly startEpoch = Date.now();
  private readonly deadline: number;
  private session?: CDPSession;
  private initial?: Memory;
  private navigations = 0;
  private frozen = false;
  private pendingInteractions = 0;
  private completion?: ReturnType<EngineeringRecorder["collect"]>;
  private timer?: ReturnType<typeof setTimeout>;
  private readonly request = (request: Request) => {
    if (performance.now() <= this.deadline)
      this.network.start(request, classify(request.url(), this.origin));
  };
  private readonly response = (response: Response) => {
    if (performance.now() <= this.deadline)
      this.network.response(response.request(), response.status());
  };
  private readonly completed = (request: Request) => {
    if (performance.now() <= this.deadline) this.network.end(request, false);
  };
  private readonly failed = (request: Request) => {
    if (performance.now() <= this.deadline) this.network.end(request, true);
  };
  private readonly navigated = () => {
    if (!this.frozen) this.navigations++;
  };
  private readonly mainFrameNavigated = (
    frame: ReturnType<Page["mainFrame"]>,
  ) => {
    if (frame === this.page.mainFrame()) this.navigated();
  };
  private constructor(
    private readonly page: Page,
    private readonly origin: string,
    private readonly journey: Journey,
    private readonly durationMs: number,
    private readonly suppliedProvenance: ReturnType<typeof provenance>,
  ) {
    this.deadline = this.started + durationMs;
  }
  static async start(
    page: Page,
    origin: string,
    journey: Journey,
    durationMs = 30_000,
    suppliedProvenance?: unknown,
  ) {
    if (
      !journeys.includes(journey) ||
      !Number.isSafeInteger(durationMs) ||
      durationMs < 1000 ||
      durationMs > 120_000
    )
      throw new Error("invalid engineering window");
    if (active.has(page))
      throw new Error("engineering page already has an active window");
    const declared = provenance(suppliedProvenance);
    const recorder = new EngineeringRecorder(
      page,
      origin,
      journey,
      durationMs,
      declared,
    );
    active.set(page, recorder.windowID);
    page.on("request", recorder.request);
    page.on("response", recorder.response);
    page.on("requestfinished", recorder.completed);
    page.on("requestfailed", recorder.failed);
    page.on("framenavigated", recorder.mainFrameNavigated);
    recorder.timer = setTimeout(() => recorder.freeze(), durationMs);
    try {
      let bootstrap = installed.get(page);
      if (!bootstrap) {
        bootstrap = page
          .addInitScript(installEngineeringBrowser)
          .then(() => undefined);
        installed.set(page, bootstrap);
      }
      if (
        !(await bounded(
          bootstrap.then(() => true),
          1000,
        ))
      )
        throw new Error("setup timeout");
      let acceptBrowserSetup = true;
      const installing = page
        .evaluate(installEngineeringBrowser, {
          start: recorder.startEpoch,
          end: recorder.startEpoch + durationMs,
          id: recorder.windowID,
        })
        .then(() => {
          if (!acceptBrowserSetup)
            void recorder.stopBrowser(recorder.startEpoch);
          return true;
        });
      const browserInstalled = await bounded(installing, 1000);
      acceptBrowserSetup = false;
      if (!browserInstalled) throw new Error("setup timeout");
      let acceptSession = true;
      const creating = page
        .context()
        .newCDPSession(page)
        .then((session) => {
          if (!acceptSession) void bounded(session.detach(), 1000);
          return session;
        });
      recorder.session = await bounded(creating, 1000);
      acceptSession = false;
      if (recorder.session) {
        await bounded(recorder.session.send("Performance.enable"), 1000);
        recorder.initial = await recorder.memory();
      }
      if (recorder.frozen || performance.now() >= recorder.deadline)
        throw new Error("engineering setup exhausted window");
      return recorder;
    } catch {
      recorder.freeze();
      await recorder.cleanup();
      throw new Error("engineering recorder setup failed");
    }
  }
  private async memory(): Promise<Memory | undefined> {
    if (!this.session) return undefined;
    const response = await bounded(
      this.session.send("Performance.getMetrics"),
      1000,
    );
    if (!response) return undefined;
    const result: Memory = {};
    for (const metric of response.metrics) {
      if (
        metricNames.includes(metric.name as (typeof metricNames)[number]) &&
        Number.isFinite(metric.value) &&
        metric.value >= 0
      )
        result[metric.name as (typeof metricNames)[number]] = metric.value;
    }
    return Object.keys(result).length ? result : undefined;
  }
  async interaction(action: () => Promise<void>) {
    if (this.frozen) throw new Error("engineering window closed");
    const started = performance.now();
    let succeeded = false;
    this.pendingInteractions++;
    try {
      await action();
      succeeded = true;
    } finally {
      if (!this.frozen && performance.now() <= this.deadline) {
        this.interactions.add(performance.now() - started, succeeded);
        this.pendingInteractions--;
      }
    }
  }
  private freeze() {
    if (this.frozen) return;
    this.frozen = true;
    clearTimeout(this.timer);
    this.page.off("request", this.request);
    this.page.off("response", this.response);
    this.page.off("requestfinished", this.completed);
    this.page.off("requestfailed", this.failed);
    this.page.off("framenavigated", this.mainFrameNavigated);
    this.network.finish();
  }
  private stopBrowser(cutoff: number) {
    return bounded(
      this.page.evaluate(
        ({ id, cutoff }) => {
          const controller = (
            window as unknown as {
              __astronomerEngineering?: {
                id: number;
                stop(cutoff: number): unknown;
              };
            }
          ).__astronomerEngineering;
          const key = "astronomer:engineering-window:v1";
          try {
            if (JSON.parse(sessionStorage.getItem(key) || "null")?.id === id)
              sessionStorage.removeItem(key);
          } catch {
            /* unavailable storage */
          }
          return controller?.id === id ? controller.stop(cutoff) : undefined;
        },
        { id: this.windowID, cutoff },
      ),
      1500,
    );
  }
  private async cleanup() {
    await this.stopBrowser(
      this.startEpoch +
        Math.min(this.durationMs, performance.now() - this.started),
    );
    if (this.session) await bounded(this.session.detach(), 1000);
    if (active.get(this.page) === this.windowID) active.delete(this.page);
  }
  finish() {
    return (this.completion ??= this.collect());
  }
  private async collect() {
    const actualDuration = Math.min(
      this.durationMs,
      performance.now() - this.started,
    );
    this.freeze(); // Window is immutable before any awaited collector work.
    const completedInteractions = this.interactions.snapshot();
    const interactionSnapshot = {
      ...completedInteractions,
      attempted: completedInteractions.attempted + this.pendingInteractions,
      unfinished: this.pendingInteractions,
    };
    const browser = sanitizeBrowserSnapshot(
      await this.stopBrowser(this.startEpoch + actualDuration),
    );
    const final = await this.memory();
    await this.cleanup();
    const deltas: Memory = {};
    for (const name of metricNames) {
      if (
        name.startsWith("JSHeap") ||
        this.navigations ||
        final?.[name] === undefined ||
        this.initial?.[name] === undefined
      )
        continue;
      if (final[name]! >= this.initial[name]!)
        deltas[name] = final[name]! - this.initial[name]!;
    }
    const network = this.network.finish();
    return {
      schema_version: "astronomer-browser-engineering/v1",
      scope: "engineering_browser_measurement",
      qualification: "not_evaluated",
      collection_status: "partial",
      journey: this.journey,
      window: {
        requested_ms: this.durationMs,
        measured_ms: actualDuration,
        navigations: this.navigations,
      },
      provenance: this.suppliedProvenance,
      network,
      interactions: interactionSnapshot,
      browser_performance: browser ?? {
        status: "missing",
        reason: "browser_collection_unavailable",
      },
      cdp: {
        status: final ? "available" : "missing",
        initial: this.initial ?? null,
        final: final ?? null,
        duration_deltas_seconds: deltas,
        delta_status: this.navigations
          ? "unavailable_navigation_reset"
          : Object.keys(deltas).length === 3
            ? "available"
            : "missing_or_reset",
        collection: "post_window_snapshot",
        heap_scope: "page_target_js_heap_not_total_memory",
      },
      limitations: [
        "engineering_only",
        "final_document_resource_timing",
        "no_react_commit_measurement",
        "provenance_not_independently_verified",
        "no_performance_budget_evaluated",
      ],
    } as const;
  }
}
