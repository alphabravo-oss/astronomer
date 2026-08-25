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

export function buildSharedLoggingURL(
  input: string | URL,
  filters: SharedLoggingFilters,
): string {
  const url = input instanceof URL ? new URL(input) : new URL(input);
  url.searchParams.set(keys.output, filters.outputId);
  url.searchParams.set(keys.query, filters.query.slice(0, 16_384));
  url.searchParams.set(
    keys.namespaces,
    filters.namespaces.slice(0, 20).join(","),
  );
  url.searchParams.set(
    keys.limit,
    String(Math.min(1000, Math.max(1, Math.trunc(filters.limit) || 100))),
  );
  if (filters.start) url.searchParams.set(keys.start, filters.start);
  else url.searchParams.delete(keys.start);
  if (filters.end) url.searchParams.set(keys.end, filters.end);
  else url.searchParams.delete(keys.end);
  return url.toString();
}

export function clearSharedLoggingURL(input: string | URL): string {
  const url = input instanceof URL ? new URL(input) : new URL(input);
  for (const key of Object.values(keys)) url.searchParams.delete(key);
  return url.toString();
}
