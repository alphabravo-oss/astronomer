import type { Page, Request, Response } from "@playwright/test";

export const BENCHMARK_SCHEMA_VERSION = "rancher-ux-benchmark/v1" as const;

export interface BrowserInteractionMetrics {
  active_ms: number;
  wall_ms: number;
  pointer_activations: number;
  keyboard_activations: number;
  route_transitions: number;
  error_count: number;
  recovery_actions: number;
  duplicate_effects: number;
}

interface BrowserBenchmarkController {
  begin(taskId: string): void;
  incrementError(): void;
  markRecoveryAction(): void;
  pauseOperatorTime(): void;
  resumeOperatorTime(): void;
  snapshot(): Omit<BrowserInteractionMetrics, "wall_ms" | "duplicate_effects">;
}

declare global {
  interface Window {
    __astronomerBenchmarkV1?: BrowserBenchmarkController;
  }
}

const STORAGE_KEY = "astronomer:rancher-ux-benchmark:v1";

/**
 * Install product-neutral interaction accounting before the first benchmark
 * navigation. The state is session-scoped so a full-page navigation does not
 * reset a task. Independent cluster/API probes still own success and duplicate
 * effect detection.
 */
export async function installBenchmarkInstrumentation(page: Page): Promise<void> {
  await page.addInitScript(({ storageKey }) => {
    type StoredState = {
      active: boolean;
      operatorActive: boolean;
      taskId: string;
      activeStartedAtEpoch: number;
      activeAccumulatedMs: number;
      pointerActivations: number;
      keyboardActivations: number;
      routeTransitions: number;
      errorCount: number;
      recoveryActions: number;
      lastUrl: string;
    };

    const blank = (): StoredState => ({
      active: false,
      operatorActive: false,
      taskId: "",
      activeStartedAtEpoch: 0,
      activeAccumulatedMs: 0,
      pointerActivations: 0,
      keyboardActivations: 0,
      routeTransitions: 0,
      errorCount: 0,
      recoveryActions: 0,
      lastUrl: window.location.href,
    });
    const load = (): StoredState => {
      try {
        const raw = window.sessionStorage.getItem(storageKey);
        return raw ? (JSON.parse(raw) as StoredState) : blank();
      } catch {
        return blank();
      }
    };
    const save = (state: StoredState): void => {
      try {
        window.sessionStorage.setItem(storageKey, JSON.stringify(state));
      } catch {
        // Evidence collection must not change product behavior when storage is
        // unavailable. The runner will reject the missing final snapshot.
      }
    };
    const mutate = (change: (state: StoredState) => void): void => {
      const state = load();
      if (!state.active) return;
      change(state);
      save(state);
    };
    const epochNow = (): number => performance.timeOrigin + performance.now();
    const pauseOperatorTime = (): void => {
      mutate((state) => {
        if (!state.operatorActive) return;
        state.activeAccumulatedMs += Math.max(0, epochNow() - state.activeStartedAtEpoch);
        state.activeStartedAtEpoch = 0;
        state.operatorActive = false;
      });
    };
    const resumeOperatorTime = (): void => {
      mutate((state) => {
        if (state.operatorActive) return;
        state.activeStartedAtEpoch = epochNow();
        state.operatorActive = true;
      });
    };
    const routeChanged = (): void => {
      mutate((state) => {
        if (state.lastUrl !== window.location.href) {
          state.routeTransitions += 1;
          state.lastUrl = window.location.href;
        }
      });
    };

    const existing = load();
    if (existing.active && existing.lastUrl !== window.location.href) {
      existing.routeTransitions += 1;
      existing.lastUrl = window.location.href;
      save(existing);
    }

    document.addEventListener(
      "pointerup",
      (event) => {
        if (event.isTrusted && event.button === 0) {
          mutate((state) => {
            state.pointerActivations += 1;
          });
        }
      },
      true,
    );
    document.addEventListener(
      "keyup",
      (event) => {
        if (event.isTrusted && (event.key === "Enter" || event.key === " ")) {
          mutate((state) => {
            state.keyboardActivations += 1;
          });
        }
      },
      true,
    );
    window.addEventListener("error", () => {
      mutate((state) => {
        state.errorCount += 1;
      });
    });
    window.addEventListener("unhandledrejection", () => {
      mutate((state) => {
        state.errorCount += 1;
      });
    });

    const pushState = window.history.pushState.bind(window.history);
    window.history.pushState = (...args) => {
      pushState(...args);
      routeChanged();
    };
    window.addEventListener("popstate", routeChanged);
    window.addEventListener("hashchange", routeChanged);
    window.addEventListener("pagehide", pauseOperatorTime);
    window.addEventListener("pageshow", resumeOperatorTime);
    document.addEventListener("visibilitychange", () => {
      if (document.visibilityState === "hidden") pauseOperatorTime();
      else resumeOperatorTime();
    });

    // A full navigation has a new performance time origin. Resume from the
    // persisted epoch-based accumulator rather than subtracting unrelated
    // performance.now() values.
    if (existing.active && document.visibilityState !== "hidden") {
      existing.operatorActive = true;
      existing.activeStartedAtEpoch = epochNow();
      save(existing);
    }

    window.__astronomerBenchmarkV1 = {
      begin(taskId: string) {
        const state = blank();
        state.active = true;
        state.operatorActive = true;
        state.taskId = taskId;
        state.activeStartedAtEpoch = epochNow();
        save(state);
      },
      incrementError() {
        mutate((state) => {
          state.errorCount += 1;
        });
      },
      markRecoveryAction() {
        mutate((state) => {
          state.recoveryActions += 1;
        });
      },
      pauseOperatorTime,
      resumeOperatorTime,
      snapshot() {
        const state = load();
        const running = state.operatorActive
          ? Math.max(0, epochNow() - state.activeStartedAtEpoch)
          : 0;
        return {
          active_ms: Math.max(0, Math.round(state.activeAccumulatedMs + running)),
          pointer_activations: state.pointerActivations,
          keyboard_activations: state.keyboardActivations,
          route_transitions: state.routeTransitions,
          error_count: state.errorCount,
          recovery_actions: state.recoveryActions,
        };
      },
    };
  }, { storageKey: STORAGE_KEY });
}

export class BenchmarkTaskRecorder {
  private wallStartedAt = 0;
  private readonly failedRequests = new WeakSet<Request>();
  private readonly failedResponses = new WeakSet<Response>();

  constructor(private readonly page: Page) {
    page.on("requestfailed", (request) => {
      if (this.failedRequests.has(request)) return;
      this.failedRequests.add(request);
      void this.incrementBrowserError();
    });
    page.on("response", (response) => {
      if (response.status() < 400 || this.failedResponses.has(response)) return;
      this.failedResponses.add(response);
      void this.incrementBrowserError();
    });
  }

  async begin(taskId: string): Promise<void> {
    if (!/^[a-z0-9][a-z0-9-]{2,63}$/.test(taskId)) {
      throw new Error(`invalid benchmark task id: ${taskId}`);
    }
    this.wallStartedAt = Date.now();
    await this.page.evaluate((id) => {
      if (!window.__astronomerBenchmarkV1) {
        throw new Error("benchmark instrumentation is not installed");
      }
      window.__astronomerBenchmarkV1.begin(id);
    }, taskId);
  }

  async markRecoveryAction(): Promise<void> {
    await this.page.evaluate(() => {
      window.__astronomerBenchmarkV1?.markRecoveryAction();
    });
  }

  async pauseForControllerWait(): Promise<void> {
    await this.page.evaluate(() => {
      window.__astronomerBenchmarkV1?.pauseOperatorTime();
    });
  }

  async resumeAfterControllerWait(): Promise<void> {
    await this.page.evaluate(() => {
      window.__astronomerBenchmarkV1?.resumeOperatorTime();
    });
  }

  async finish(duplicateEffects: number): Promise<BrowserInteractionMetrics> {
    await this.pauseForControllerWait();
    const browser = await this.page.evaluate(() => {
      if (!window.__astronomerBenchmarkV1) {
        throw new Error("benchmark instrumentation is not installed");
      }
      return window.__astronomerBenchmarkV1.snapshot();
    });
    const metrics: BrowserInteractionMetrics = {
      ...browser,
      wall_ms: Math.max(0, Date.now() - this.wallStartedAt),
      duplicate_effects: duplicateEffects,
    };
    assertBenchmarkMetrics(metrics);
    return metrics;
  }

  private async incrementBrowserError(): Promise<void> {
    if (this.page.isClosed()) return;
    await this.page
      .evaluate(() => window.__astronomerBenchmarkV1?.incrementError())
      .catch(() => undefined);
  }
}

export function assertBenchmarkMetrics(
  metrics: BrowserInteractionMetrics,
): void {
  for (const [name, value] of Object.entries(metrics)) {
    if (!Number.isSafeInteger(value) || value < 0) {
      throw new Error(`${name} must be a non-negative safe integer`);
    }
  }
}
