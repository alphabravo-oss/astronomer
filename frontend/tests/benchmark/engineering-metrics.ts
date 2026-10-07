/** Engineering evidence only. No endpoint identities or arbitrary labels are retained. */
export const groups = [
  "clusters",
  "resources",
  "inventory",
  "deployments",
  "search",
  "auth",
  "stream",
  "assets",
  "other_api",
  "other",
] as const;
export type Group = (typeof groups)[number];
export const journeys = [
  "cluster_list",
  "resource_list",
  "delivery_inventory",
  "scoped_search",
  "two_tabs",
  "rapid_scope",
  "stream_open",
  "stream_fallback",
  "stream_recovered",
  "denied",
] as const;
export type Journey = (typeof journeys)[number];
export const MAX_ACTIVE = 2048;
export const MAX_SAMPLES = 256;
export function classify(url: string, origin: string): Group {
  try {
    const parsed = new URL(url);
    if (parsed.origin !== origin) return "other";
    const path = parsed.pathname;
    if (/^\/api\/v1\/events\/stream\/?$/.test(path)) return "stream";
    if (/^\/api\/v1\/auth\//.test(path)) return "auth";
    if (/^\/api\/v1\/clusters\/?$/.test(path)) return "clusters";
    if (/^\/api\/v1\/delivery\/clusters\/[^/]+\/inventory\/?$/.test(path))
      return "inventory";
    if (/^\/api\/v1\/delivery\/deployments\//.test(path)) return "deployments";
    if (/^\/api\/v1\/resources\/search\/?$/.test(path)) return "search";
    if (
      /^\/api\/v1\/clusters\/[^/]+\/(k8s|resources|resource-counts)\//.test(
        path,
      )
    )
      return "resources";
    if (path.startsWith("/api/")) return "other_api";
    return /\.(js|css|woff2?|png|svg)$/.test(path) ? "assets" : "other";
  } catch {
    return "other";
  }
}
export class WindowCounters {
  private active = new Map<object, Group>();
  private frozen = false;
  private droppedKeys = new WeakSet<object>();
  readonly rows = Object.fromEntries(
    groups.map((group) => [
      group,
      {
        started: 0,
        completed: 0,
        failed: 0,
        unfinished: 0,
        http_2xx: 0,
        http_3xx: 0,
        http_4xx: 0,
        http_5xx: 0,
        http_other: 0,
      },
    ]),
  ) as Record<
    Group,
    {
      started: number;
      completed: number;
      failed: number;
      unfinished: number;
      http_2xx: number;
      http_3xx: number;
      http_4xx: number;
      http_5xx: number;
      http_other: number;
    }
  >;
  dropped = 0;
  carryInCompletions = 0;
  start(key: object, group: Group) {
    if (this.frozen) return;
    if (this.active.size >= MAX_ACTIVE) {
      this.dropped++;
      this.droppedKeys.add(key);
      return;
    }
    this.active.set(key, group);
    this.rows[group].started++;
  }
  response(key: object, status: number) {
    if (this.frozen) return;
    const group = this.active.get(key);
    if (!group) return;
    const field =
      status >= 200 && status < 300
        ? "http_2xx"
        : status >= 300 && status < 400
          ? "http_3xx"
          : status >= 400 && status < 500
            ? "http_4xx"
            : status >= 500 && status < 600
              ? "http_5xx"
              : "http_other";
    this.rows[group][field]++;
  }
  end(key: object, failed: boolean) {
    if (this.frozen) return;
    const group = this.active.get(key);
    if (!group) {
      if (!this.droppedKeys.has(key)) this.carryInCompletions++;
      return;
    }
    this.rows[group][failed ? "failed" : "completed"]++;
    this.active.delete(key);
  }
  finish() {
    if (!this.frozen) {
      this.frozen = true;
      for (const group of this.active.values()) this.rows[group].unfinished++;
      this.active.clear();
    }
    return {
      groups: structuredClone(this.rows),
      dropped_requests: this.dropped,
      // Pre-attachment requests are outside the tracked-start denominator.
      carry_in_completions: this.carryInCompletions,
      carry_in_unfinished_status: "unknown",
    };
  }
}
export class InteractionSamples {
  attempted = 0;
  failed = 0;
  dropped = 0;
  private samples: number[] = [];
  add(duration: number, succeeded: boolean) {
    this.attempted++;
    if (!succeeded || !Number.isFinite(duration) || duration < 0) {
      this.failed++;
      return;
    }
    if (this.samples.length >= MAX_SAMPLES) {
      this.dropped++;
      return;
    }
    this.samples.push(duration);
  }
  snapshot() {
    const values = [...this.samples].sort((a, b) => a - b);
    const percentile = (p: number) =>
      this.dropped || !values.length
        ? null
        : values[Math.ceil(p * values.length) - 1];
    return {
      attempted: this.attempted,
      completed: this.attempted - this.failed,
      failed: this.failed,
      retained_samples: values.length,
      dropped_samples: this.dropped,
      p50_ms: percentile(0.5),
      p95_ms: percentile(0.95),
      max_ms: this.dropped || !values.length ? null : values[values.length - 1],
    };
  }
}
export async function bounded<T>(
  promise: Promise<T>,
  timeoutMs: number,
): Promise<T | undefined> {
  let timer: number | undefined;
  try {
    return await Promise.race([
      promise.catch(() => undefined),
      new Promise<undefined>((resolve) => {
        timer = globalThis.setTimeout(resolve, timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

export interface EngineeringProvenance {
  source_commit: string;
  images_sha256: string;
  chart_values_sha256: string;
  dataset_sha256: string;
  hardware_sha256: string;
}
export function provenance(value?: unknown) {
  if (value === undefined)
    return {
      status: "missing" as const,
      reason: "matched_estate_provenance_not_supplied" as const,
    };
  if (!value || typeof value !== "object")
    throw new Error("invalid engineering provenance");
  const raw = value as Record<string, unknown>;
  const keys = [
    "source_commit",
    "images_sha256",
    "chart_values_sha256",
    "dataset_sha256",
    "hardware_sha256",
  ] as const;
  if (
    Object.keys(raw).length !== keys.length ||
    keys.some(
      (key) =>
        typeof raw[key] !== "string" ||
        !(
          key === "source_commit"
            ? /^(?:[a-f0-9]{40}|[a-f0-9]{64})$/
            : /^[a-f0-9]{64}$/
        ).test(raw[key] as string),
    )
  )
    throw new Error("invalid engineering provenance");
  return {
    status: "supplied_unverified" as const,
    source_commit: raw.source_commit as string,
    images_sha256: raw.images_sha256 as string,
    chart_values_sha256: raw.chart_values_sha256 as string,
    dataset_sha256: raw.dataset_sha256 as string,
    hardware_sha256: raw.hardware_sha256 as string,
  };
}

/** Validation only: payload identities are transient and never enter the artifact. */
export function completeSearchFixture(
  payload: unknown,
  cluster: string,
  namespace: string,
  name: string,
): boolean {
  if (!payload || typeof payload !== "object") return false;
  const value = payload as Record<string, unknown>;
  if (
    value.clusters_failed !== 0 ||
    value.truncated !== false ||
    !Array.isArray(value.errors) ||
    value.errors.length ||
    !Array.isArray(value.results)
  )
    return false;
  return value.results.some(
    (row) =>
      row?.cluster_id === cluster &&
      row?.namespace === namespace &&
      row?.name === name,
  );
}
