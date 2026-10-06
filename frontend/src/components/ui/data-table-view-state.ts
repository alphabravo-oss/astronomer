/**
 * Serializable DataTable view state: what a saved view stores and what the
 * URL carries. Pure helpers; no React.
 */
import type { ColumnPinningState } from "@/components/ui/data-table-layout";
import type { OpenAPIComponents } from "@/types/openapi.generated";

export type TableViewState = OpenAPIComponents["schemas"]["TableViewState"];

export const VIEW_STATE_VERSION = 1;

const isStringArray = (value: unknown): value is string[] =>
  Array.isArray(value) && value.every((v) => typeof v === "string");

/**
 * Keep only well-formed fields that refer to real columns, so a stale URL or
 * saved view can never poison table state.
 */
export function sanitizeViewState(
  raw: unknown,
  validKeys: ReadonlySet<string>,
): TableViewState {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return {};
  const input = raw as Record<string, unknown>;
  const out: TableViewState = { v: VIEW_STATE_VERSION };
  if (typeof input.search === "string" && input.search) {
    out.search = input.search.slice(0, 200);
  }
  if (
    input.filters &&
    typeof input.filters === "object" &&
    !Array.isArray(input.filters)
  ) {
    const filters = Object.fromEntries(
      Object.entries(input.filters as Record<string, unknown>).filter(
        ([key, value]) =>
          validKeys.has(key) && isStringArray(value) && value.length > 0,
      ),
    ) as Record<string, string[]>;
    if (Object.keys(filters).length) out.filters = filters;
  }
  if (Array.isArray(input.sort)) {
    const sort = input.sort.filter(
      (s): s is { id: string; desc: boolean } =>
        !!s &&
        typeof s === "object" &&
        typeof (s as { id?: unknown }).id === "string" &&
        validKeys.has((s as { id: string }).id) &&
        typeof (s as { desc?: unknown }).desc === "boolean",
    );
    if (sort.length) out.sort = sort.map(({ id, desc }) => ({ id, desc }));
  }
  if (isStringArray(input.hidden)) {
    const hidden = input.hidden.filter((k) => validKeys.has(k));
    if (hidden.length) out.hidden = hidden;
  }
  if (isStringArray(input.order)) {
    const order = input.order.filter((k) => validKeys.has(k));
    if (order.length) out.order = order;
  }
  const pin = input.pinning as Partial<ColumnPinningState> | undefined;
  if (pin && typeof pin === "object") {
    const start = isStringArray(pin.start)
      ? pin.start.filter((k) => validKeys.has(k))
      : [];
    const end = isStringArray(pin.end)
      ? pin.end.filter((k) => validKeys.has(k) && !start.includes(k))
      : [];
    out.pinning = { start, end };
  }
  return out;
}

export function isEmptyViewState(state: TableViewState): boolean {
  return (
    !state.search &&
    !state.filters &&
    !state.sort &&
    !state.hidden &&
    !state.order &&
    !state.pinning
  );
}

/** Compact JSON for a URL query value; empty string when there is no state. */
export function encodeViewState(state: TableViewState): string {
  return isEmptyViewState(state) ? "" : JSON.stringify(state);
}

export function decodeViewState(
  raw: string | null | undefined,
  validKeys: ReadonlySet<string>,
): TableViewState | null {
  if (!raw) return null;
  try {
    const state = sanitizeViewState(JSON.parse(raw), validKeys);
    return isEmptyViewState(state) ? null : state;
  } catch {
    return null;
  }
}

/** Query-param name for a table's URL state. */
export const viewParamName = (persistKey: string) => `tv-${persistKey}`;

/** Server table_key: lowercase, restricted charset, max 128 chars. */
export function viewTableKey(persistKey: string): string {
  const cleaned = persistKey
    .toLowerCase()
    .replace(/[^a-z0-9:._/-]+/g, "-")
    .replace(/^[^a-z0-9]+/, "");
  return (cleaned || "table").slice(0, 128);
}
