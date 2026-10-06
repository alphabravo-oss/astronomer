/**
 * Column ordering, pinning placement and cell styles for DataTable. Split from
 * data-table-layout.ts, which re-exports everything here.
 */
import type { CSSProperties } from "react";
import {
  parsePx,
  resolveColumnLayout,
  type ColumnLayoutInput,
  type ResolvedColumnLayout,
} from "@/components/ui/data-table-layout";

// ---------------------------------------------------------------------------
// Ordering and pinning
// ---------------------------------------------------------------------------

export interface ColumnPinningState {
  start: string[];
  end: string[];
}

export const EMPTY_PINNING: ColumnPinningState = { start: [], end: [] };

/**
 * Default pinning: the `name` column left when the table scrolls
 * horizontally, plus any column whose kind or `pin` asks for a side
 * (actions end). The selection column is pinned by the renderer itself.
 */
export function defaultPinning(
  columns: ReadonlyArray<ColumnLayoutInput>,
  layout: "fit" | "scroll",
): ColumnPinningState {
  const start: string[] = [];
  const end: string[] = [];
  for (const col of columns) {
    const resolved = resolveColumnLayout(col);
    if (resolved.pin === "end") end.push(col.key);
    else if (resolved.pin === "start") start.push(col.key);
    else if (layout === "scroll" && resolved.kind === "name" && !start.length)
      start.push(col.key);
  }
  return { start, end };
}

/** Pinning actually in effect: unknown ids dropped, no key on both sides. */
export function normalizePinning(
  pinning: ColumnPinningState,
  keys: ReadonlySet<string>,
): ColumnPinningState {
  const start = pinning.start.filter((k) => keys.has(k));
  const end = pinning.end.filter((k) => keys.has(k) && !start.includes(k));
  return { start, end };
}

/**
 * Final left-to-right order: start-pinned, then the rest in `order`
 * (columns missing from `order` keep their declared position after the
 * ordered ones), then end-pinned.
 */
export function orderColumns<T extends { key: string }>(
  columns: ReadonlyArray<T>,
  order: ReadonlyArray<string>,
  pinning: ColumnPinningState,
): T[] {
  const byKey = new Map(columns.map((c) => [c.key, c]));
  const pin = normalizePinning(pinning, new Set(byKey.keys()));
  const pinned = new Set([...pin.start, ...pin.end]);
  const rank = new Map(order.map((key, i) => [key, i]));
  const center = columns
    .filter((c) => !pinned.has(c.key))
    .map((c, declared) => ({ c, declared }))
    .sort((a, b) => {
      const ra = rank.get(a.c.key);
      const rb = rank.get(b.c.key);
      if (ra !== undefined && rb !== undefined) return ra - rb;
      if (ra !== undefined) return -1;
      if (rb !== undefined) return 1;
      return a.declared - b.declared;
    })
    .map(({ c }) => c);
  return [
    ...pin.start.map((k) => byKey.get(k) as T),
    ...center,
    ...pin.end.map((k) => byKey.get(k) as T),
  ];
}

/** Move `key` one slot among the unpinned columns of `order`. */
export function moveColumn(
  visibleOrder: ReadonlyArray<string>,
  key: string,
  direction: -1 | 1,
): string[] {
  const index = visibleOrder.indexOf(key);
  const target = index + direction;
  if (index < 0 || target < 0 || target >= visibleOrder.length) {
    return [...visibleOrder];
  }
  const next = [...visibleOrder];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

/**
 * Move `key` one step among `movable` columns inside the complete `fullOrder`,
 * leaving non-movable columns (row actions, structural columns) where they
 * are. Returns a complete order.
 */
export function reorderWithin(
  fullOrder: ReadonlyArray<string>,
  movable: ReadonlySet<string>,
  key: string,
  direction: -1 | 1,
): string[] {
  const slots = fullOrder.flatMap((k, i) => (movable.has(k) ? [i] : []));
  const keys = slots.map((i) => fullOrder[i]);
  const moved = moveColumn(keys, key, direction);
  const next = [...fullOrder];
  slots.forEach((slot, i) => {
    next[slot] = moved[i];
  });
  return next;
}

export const SELECT_COLUMN_WIDTH = 40;
/** Width reserved for a pinned column that declares no pixel size. */
export const DEFAULT_PINNED_WIDTH = 160;

export interface PinnedPlacement {
  side: "start" | "end";
  offset: number;
  width: number;
  /** Last start-pinned or first end-pinned column: draws the divider. */
  edge: boolean;
}

/**
 * Sticky offsets for pinned columns in an already-ordered list. A pinned
 * column always gets an explicit width so the offsets of its neighbours are
 * deterministic.
 */
export function pinnedPlacements(
  ordered: ReadonlyArray<ColumnLayoutInput>,
  pinning: ColumnPinningState,
  leadWidth: number,
): Map<string, PinnedPlacement> {
  const result = new Map<string, PinnedPlacement>();
  const widthOf = (col: ColumnLayoutInput) => {
    const r = resolveColumnLayout(col);
    return r.size ?? parsePx(r.cssWidth) ?? DEFAULT_PINNED_WIDTH;
  };
  const byKey = new Map(ordered.map((c) => [c.key, c]));
  let left = leadWidth;
  pinning.start.forEach((key, i) => {
    const col = byKey.get(key);
    if (!col) return;
    const width = widthOf(col);
    result.set(key, {
      side: "start",
      offset: left,
      width,
      edge: i === pinning.start.length - 1,
    });
    left += width;
  });
  let right = 0;
  [...pinning.end].reverse().forEach((key, i, arr) => {
    const col = byKey.get(key);
    if (!col) return;
    const width = widthOf(col);
    result.set(key, {
      side: "end",
      offset: right,
      width,
      edge: i === arr.length - 1,
    });
    right += width;
  });
  return result;
}

// ---------------------------------------------------------------------------
// Style helpers shared by the semantic table and the virtualized grid
// ---------------------------------------------------------------------------

/** Inline sizing for a header or data cell in the semantic table. */
export function tableCellStyle(
  layout: ResolvedColumnLayout,
  options: { resizedWidth?: number; pinned?: PinnedPlacement },
): CSSProperties {
  const style: CSSProperties = {};
  if (options.pinned) {
    style.width = options.pinned.width;
    style.minWidth = options.pinned.width;
    style.maxWidth = options.pinned.width;
    style[options.pinned.side === "start" ? "left" : "right"] =
      options.pinned.offset;
    style.position = "sticky";
    style.zIndex = "var(--z-sticky)" as unknown as number;
    return style;
  }
  if (options.resizedWidth !== undefined) {
    style.width = options.resizedWidth;
  } else if (layout.cssWidth) {
    style.width = layout.cssWidth;
  } else if (layout.sized && !layout.grow && layout.size !== undefined) {
    style.width = layout.size;
  }
  if (layout.sized) {
    style.minWidth = layout.minSize || undefined;
    if (layout.maxSize !== undefined && !layout.grow) {
      style.maxWidth = layout.maxSize;
    }
  }
  return style;
}

/** Inline sizing for a cell of the virtualized flex grid. */
export function flexCellStyle(
  layout: ResolvedColumnLayout,
  options: {
    resizedWidth?: number;
    pinned?: PinnedPlacement;
    scroll: boolean;
  },
): CSSProperties {
  if (options.pinned) {
    const w = options.pinned.width;
    return {
      width: w,
      flex: `0 0 ${layout.cssWidth ?? `${w}px`}`,
      minWidth: w,
      position: "sticky",
      [options.pinned.side === "start" ? "left" : "right"]:
        options.pinned.offset,
      zIndex: "var(--z-sticky)" as unknown as number,
    };
  }
  const fixed = options.resizedWidth ?? layout.size ?? parsePx(layout.cssWidth);
  if (layout.grow && options.resizedWidth === undefined) {
    return {
      flex: `1 1 ${layout.size ?? 0}px`,
      minWidth: layout.minSize || 0,
    };
  }
  if (fixed !== undefined && (layout.sized || options.resizedWidth)) {
    return options.scroll || options.resizedWidth !== undefined
      ? { width: fixed, flex: `0 0 ${fixed}px`, minWidth: fixed }
      : {
          width: fixed,
          flex: `0 1 ${fixed}px`,
          minWidth: layout.minSize || 0,
        };
  }
  if (layout.cssWidth) {
    const width = layout.cssWidth;
    return options.scroll
      ? { width, flex: `0 0 ${width}`, minWidth: width }
      : { width, flex: `0 1 ${width}`, minWidth: 0 };
  }
  return { flex: "1 1 0", minWidth: 0 };
}
