// Recursively rewrite snake_case keys to camelCase for explicitly mapped
// feature payloads. The shared HTTP transport deliberately never calls this.

function snakeToCamel(s: string): string {
  return s.replace(/_([a-z0-9])/g, (_, ch) => ch.toUpperCase());
}

export function camelizeKeys<T = unknown>(value: T): T {
  if (Array.isArray(value)) return value.map(camelizeKeys) as unknown as T;
  if (
    value &&
    typeof value === "object" &&
    Object.getPrototypeOf(value) === Object.prototype
  ) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[snakeToCamel(k)] = camelizeKeys(v);
    }
    return out as unknown as T;
  }
  return value;
}
