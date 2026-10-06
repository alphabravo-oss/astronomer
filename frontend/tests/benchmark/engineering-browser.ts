/** Serialized into the page by Playwright. Retains numeric samples only, never names/URLs. */
export function installEngineeringBrowser(supplied?: {
  start: number;
  end: number;
  id: number;
}) {
  const key = "astronomer:engineering-window:v1";
  let bounds = supplied;
  try {
    const current = JSON.parse(sessionStorage.getItem(key) || "null");
    if (supplied && current?.id > supplied.id) return;
    if (!bounds)
      bounds = JSON.parse(sessionStorage.getItem(key) || "null") ?? undefined;
    if (
      !bounds ||
      !Number.isSafeInteger(bounds.id) ||
      bounds.id <= 0 ||
      bounds.end <= bounds.start ||
      !Number.isFinite(bounds.start) ||
      !Number.isFinite(bounds.end) ||
      bounds.end <= Date.now() ||
      bounds.end - bounds.start > 120_000
    )
      return;
    (
      window as unknown as {
        __astronomerEngineering?: { stop(cutoff: number): unknown };
      }
    ).__astronomerEngineering?.stop(Date.now());
    if (supplied) sessionStorage.setItem(key, JSON.stringify(bounds));
  } catch {
    return;
  }
  const windowBounds = bounds;
  type Sample = {
    start: number;
    end: number;
    type: "resource" | "longtask";
    bytes: number[] | null;
  };
  const samples: Sample[] = [];
  const visibility: { at: number; visible: boolean }[] = [];
  let dropped = 0,
    visibilityDropped = 0,
    stopped = false;
  const observers: PerformanceObserver[] = [];
  const supported =
    typeof PerformanceObserver !== "undefined"
      ? PerformanceObserver.supportedEntryTypes
      : [];
  const capabilities = {
    resource_timing: false,
    navigation_timing: false,
    long_tasks: false,
    react_commit_timing: false,
  };
  const recordVisibility = () => {
    if (stopped) return;
    if (visibility.length === 256) {
      visibilityDropped++;
      return;
    }
    visibility.push({
      at: Date.now(),
      visible: document.visibilityState === "visible",
    });
  };
  recordVisibility();
  document.addEventListener("visibilitychange", recordVisibility);
  function fold(entries: PerformanceEntry[]) {
    if (stopped) return;
    for (const entry of entries) {
      if (samples.length === 4096) {
        dropped++;
        continue;
      }
      const start = performance.timeOrigin + entry.startTime;
      const end = start + entry.duration;
      if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
        dropped++;
        continue;
      }
      if (entry.entryType === "longtask") {
        samples.push({ start, end, type: "longtask", bytes: null });
        continue;
      }
      const resource = entry as PerformanceResourceTiming;
      let sameOrigin = false;
      try {
        sameOrigin = new URL(resource.name).origin === location.origin;
      } catch {
        /* opaque */
      }
      const bytes = [
        resource.transferSize,
        resource.encodedBodySize,
        resource.decodedBodySize,
      ];
      samples.push({
        start,
        end,
        type: "resource",
        bytes:
          sameOrigin &&
          bytes.every(
            (value) =>
              Number.isSafeInteger(value) && value >= 0 && value < 1e12,
          )
            ? bytes
            : null,
      });
    }
  }
  for (const [type, capability] of [
    ["resource", "resource_timing"],
    ["navigation", "navigation_timing"],
    ["longtask", "long_tasks"],
  ] as const) {
    if (!supported.includes(type)) continue;
    try {
      const observer = new PerformanceObserver((list) =>
        fold(list.getEntries()),
      );
      observer.observe({ type, buffered: true });
      observers.push(observer);
      capabilities[capability] = true;
    } catch {
      /* unsupported observer stays false */
    }
  }
  const stop = (requestedCutoff = windowBounds.end) => {
    if (!stopped) {
      for (const observer of observers) {
        fold(observer.takeRecords());
        observer.disconnect();
      }
      stopped = true;
      document.removeEventListener("visibilitychange", recordVisibility);
    }
    const cutoff = Math.min(windowBounds.end, requestedCutoff);
    const counters = {
      completed_entries: 0,
      transfer_bytes: 0,
      encoded_body_bytes: 0,
      decoded_body_bytes: 0,
      opaque_entries: 0,
      excluded_boundary_entries: 0,
      dropped_entries: dropped,
      long_tasks: 0,
      long_task_ms: 0,
      visibility_visible: 0,
      visibility_hidden: 0,
      visibility_dropped: visibilityDropped,
    };
    for (const sample of samples) {
      if (sample.start < windowBounds.start || sample.end > cutoff) {
        counters.excluded_boundary_entries++;
        continue;
      }
      if (sample.type === "longtask") {
        counters.long_tasks++;
        counters.long_task_ms += sample.end - sample.start;
      } else if (!sample.bytes) counters.opaque_entries++;
      else {
        counters.completed_entries++;
        counters.transfer_bytes += sample.bytes[0];
        counters.encoded_body_bytes += sample.bytes[1];
        counters.decoded_body_bytes += sample.bytes[2];
      }
    }
    let last: "visible" | "hidden" | "unknown" = "unknown";
    for (const event of visibility) {
      if (event.at > cutoff) continue;
      counters[event.visible ? "visibility_visible" : "visibility_hidden"]++;
      last = event.visible ? "visible" : "hidden";
    }
    return {
      ...counters,
      capabilities,
      coverage: "final_main_document_only" as const,
      visibility_at_end: last,
    };
  };
  const timer = window.setTimeout(
    () => stop(),
    Math.max(0, windowBounds.end - Date.now()),
  );
  const api = {
    id: windowBounds.id,
    stop: (cutoff?: number) => {
      window.clearTimeout(timer);
      return stop(cutoff);
    },
  };
  (
    window as unknown as { __astronomerEngineering?: typeof api }
  ).__astronomerEngineering = api;
}
// Explicit projection prevents an unexpected page result from entering artifacts.
const counterFields = [
  "completed_entries",
  "transfer_bytes",
  "encoded_body_bytes",
  "decoded_body_bytes",
  "opaque_entries",
  "excluded_boundary_entries",
  "dropped_entries",
  "long_tasks",
  "long_task_ms",
  "visibility_visible",
  "visibility_hidden",
  "visibility_dropped",
] as const;
export function sanitizeBrowserSnapshot(value: unknown) {
  if (!value || typeof value !== "object") return undefined;
  const raw = value as Record<string, unknown>;
  const counts: Partial<Record<(typeof counterFields)[number], number>> = {};
  for (const key of counterFields) {
    if (
      typeof raw[key] !== "number" ||
      !Number.isFinite(raw[key]) ||
      raw[key] < 0 ||
      raw[key] > Number.MAX_SAFE_INTEGER
    )
      return undefined;
    counts[key] = raw[key];
  }
  const caps = raw.capabilities as Record<string, unknown> | undefined;
  return {
    ...counts,
    coverage: "final_main_document_only",
    visibility_at_end:
      raw.visibility_at_end === "visible"
        ? "visible"
        : raw.visibility_at_end === "hidden"
          ? "hidden"
          : "unknown",
    capabilities: {
      resource_timing: caps?.resource_timing === true,
      navigation_timing: caps?.navigation_timing === true,
      long_tasks: caps?.long_tasks === true,
      react_commit_timing: false,
    },
  };
}
