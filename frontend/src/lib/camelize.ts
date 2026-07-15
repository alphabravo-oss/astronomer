// Recursively rewrite snake_case keys to camelCase. The API returns snake_case,
// but the frontend's types are camelCase. Doing this once at the transport layer
// (axios response interceptor in lib/api.ts) keeps each call site untouched.

function snakeToCamel(s: string): string {
  return s.replace(/_([a-z0-9])/g, (_, ch) => ch.toUpperCase());
}

export function camelizeKeys<T = unknown>(value: T): T {
  if (Array.isArray(value)) return value.map(camelizeKeys) as unknown as T;
  if (value && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype) {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[snakeToCamel(k)] = camelizeKeys(v);
    }
    return out as unknown as T;
  }
  return value;
}
