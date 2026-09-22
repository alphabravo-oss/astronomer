export interface SharedLoggingFilters {
  outputId: string;
  query: string;
  namespaces: string[];
  limit: number;
  start?: string;
  end?: string;
}

const keys = {
  output: "log_output",
  query: "log_query",
  namespaces: "log_namespaces",
  limit: "log_limit",
  start: "log_start",
  end: "log_end",
} as const;

/** The shared-logging query param names, for callers that clear them via router navigation. */
export const SHARED_LOGGING_PARAM_KEYS: readonly string[] =
  Object.values(keys);

export function parseSharedLoggingFilters(
  input: string | URL,
): SharedLoggingFilters | null {
  const url = input instanceof URL ? input : new URL(input, "http://localhost");
  const outputId = url.searchParams.get(keys.output)?.trim() || "";
  if (!outputId) return null;
  const rawLimit = Number(url.searchParams.get(keys.limit) || 100);
  const limit = Number.isFinite(rawLimit)
    ? Math.min(1000, Math.max(1, Math.trunc(rawLimit)))
    : 100;
  const query = (url.searchParams.get(keys.query) || "").slice(0, 16_384);
  const namespaces = (url.searchParams.get(keys.namespaces) || "")
    .split(",")
    .map((value) => value.trim())
    .filter(Boolean)
    .slice(0, 20);
  const start = url.searchParams.get(keys.start)?.trim() || undefined;
  const end = url.searchParams.get(keys.end)?.trim() || undefined;
  return { outputId, query, namespaces, limit, start, end };
}

/**
 * The shared-logging query params for `filters`, as plain string values (an
 * absent optional field maps to `undefined` so a router `search` reducer can
 * delete rather than stringify it). Used both by `buildSharedLoggingURL` and
 * by callers that update the URL via router navigation instead of a raw
 * `URL`/`history` write.
 */
export function sharedLoggingSearchParams(
  filters: SharedLoggingFilters,
): Record<string, string | undefined> {
  return {
    [keys.output]: filters.outputId,
    [keys.query]: filters.query.slice(0, 16_384),
    [keys.namespaces]: filters.namespaces.slice(0, 20).join(","),
    [keys.limit]: String(
      Math.min(1000, Math.max(1, Math.trunc(filters.limit) || 100)),
    ),
    [keys.start]: filters.start || undefined,
    [keys.end]: filters.end || undefined,
  };
}

export function buildSharedLoggingURL(
  input: string | URL,
  filters: SharedLoggingFilters,
): string {
  const url = input instanceof URL ? new URL(input) : new URL(input);
  for (const [key, value] of Object.entries(
    sharedLoggingSearchParams(filters),
  )) {
    if (value === undefined) url.searchParams.delete(key);
    else url.searchParams.set(key, value);
  }
  return url.toString();
}

export function clearSharedLoggingURL(input: string | URL): string {
  const url = input instanceof URL ? new URL(input) : new URL(input);
  for (const key of Object.values(keys)) url.searchParams.delete(key);
  return url.toString();
}
