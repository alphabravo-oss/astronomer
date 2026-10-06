/**
 * Column layout model for DataTable: semantic column kinds, sizing math,
 * ordering and pinning. Everything here is pure so it can be unit tested
 * without rendering.
 */
import type { CSSProperties } from "react";

export type ColumnKind =
  | "name"
  | "text"
  | "status"
  | "badge"
  | "count"
  | "percent"
  | "age"
  | "date"
  | "version"
  | "id"
  | "bytes"
  | "actions"
  | "select";

/**
 * - `truncate`: one line, ellipsis, full value in a Tooltip when clipped.
 * - `wrap-2`: wraps up to two lines, then truncates (Tooltip when clipped).
 * - `nowrap`: never truncates; the column minimum is wide enough for the value.
 * - `middle`: monospace id, middle-ellipsis, click copies the full value.
 * - `fixed`: structural column (actions, selection).
 * - `wrap`: legacy `wrap` columns: free multi-line content.
 * - `legacy`: today's behaviour for columns that declare no kind.
 */
export type OverflowPolicy =
  "truncate" | "wrap-2" | "nowrap" | "middle" | "fixed" | "wrap" | "legacy";

export interface ColumnKindSpec {
  /** Preferred width in px. */
  size: number;
  /** Smallest width in px (before the header-label floor is applied). */
  minSize: number;
  maxSize?: number;
  align: "left" | "center" | "right";
  grow?: boolean;
  overflow: OverflowPolicy;
  /** Right-aligned tabular numerals. */
  numeric?: boolean;
  mono?: boolean;
  pin?: "start" | "end";
}

export const COLUMN_KINDS: Readonly<Record<ColumnKind, ColumnKindSpec>> = {
  select: {
    size: 40,
    minSize: 40,
    maxSize: 40,
    align: "left",
    overflow: "fixed",
    pin: "start",
  },
  name: {
    size: 240,
    minSize: 160,
    align: "left",
    grow: true,
    overflow: "truncate",
  },
  text: {
    size: 200,
    minSize: 120,
    maxSize: 480,
    align: "left",
    overflow: "wrap-2",
  },
  status: {
    size: 120,
    minSize: 96,
    maxSize: 180,
    align: "left",
    overflow: "nowrap",
  },
  badge: {
    size: 120,
    minSize: 88,
    maxSize: 200,
    align: "left",
    overflow: "nowrap",
  },
  count: {
    size: 80,
    minSize: 64,
    maxSize: 120,
    align: "right",
    overflow: "nowrap",
    numeric: true,
  },
  percent: {
    size: 88,
    minSize: 72,
    maxSize: 120,
    align: "right",
    overflow: "nowrap",
    numeric: true,
  },
  age: {
    size: 88,
    minSize: 72,
    maxSize: 120,
    align: "left",
    overflow: "nowrap",
  },
  date: {
    size: 160,
    minSize: 128,
    maxSize: 220,
    align: "left",
    overflow: "nowrap",
  },
  version: {
    size: 112,
    minSize: 88,
    maxSize: 180,
    align: "left",
    overflow: "nowrap",
  },
  id: {
    size: 160,
    minSize: 112,
    maxSize: 280,
    align: "left",
    overflow: "middle",
    mono: true,
  },
  bytes: {
    size: 96,
    minSize: 72,
    maxSize: 140,
    align: "right",
    overflow: "nowrap",
    numeric: true,
  },
  actions: {
    size: 48,
    minSize: 48,
    maxSize: 48,
    align: "right",
    overflow: "fixed",
    pin: "end",
  },
};

/** Sizing/appearance inputs shared with `Column<T>`. */
export interface ColumnLayoutInput {
  key: string;
  header: string;
  kind?: ColumnKind;
  rowActions?: boolean;
  /** Legacy CSS width such as "8rem". */
  width?: string;
  size?: number;
  minSize?: number;
  maxSize?: number;
  grow?: boolean;
  align?: "left" | "center" | "right";
  wrap?: boolean;
  sortable?: boolean;
  pin?: "start" | "end" | false;
}

export interface ResolvedColumnLayout {
  kind?: ColumnKind;
  /** True when the column has a declared pixel size (kind or explicit). */
  sized: boolean;
  size?: number;
  minSize: number;
  maxSize?: number;
  grow: boolean;
  align: "left" | "center" | "right";
  overflow: OverflowPolicy;
  numeric: boolean;
  mono: boolean;
  /** Legacy CSS width; takes the place of `size` when set. */
  cssWidth?: string;
  /** Default pin side from the kind or the column's own `pin`. */
  pin?: "start" | "end";
}

const HEADER_CHAR_PX = 7;
const HEADER_PADDING_PX = 32;
const SORT_ICON_PX = 24;

/**
 * Smallest width that shows the whole header label plus its sort icon, so
 * header text never wraps or clips.
 */
export function headerFloor(col: {
  header: string;
  rowActions?: boolean;
  sortable?: boolean;
  kind?: ColumnKind;
}): number {
  if (col.rowActions || col.kind === "actions" || col.kind === "select") {
    return 0;
  }
  const label = col.header.trim();
  if (!label) return 0;
  const sortable = col.sortable !== false;
  return (
    Math.ceil(label.length * HEADER_CHAR_PX) +
    HEADER_PADDING_PX +
    (sortable ? SORT_ICON_PX : 0)
  );
}

/**
 * Resolve a column's layout. Explicit `width`/`size`/`minSize`/`maxSize`/
 * `align`/`grow` always beat the kind default. A column with neither a kind
 * nor a width keeps today's equal-share behavior (`overflow: "legacy"`).
 */
export function resolveColumnLayout(
  col: ColumnLayoutInput,
): ResolvedColumnLayout {
  const kind = col.kind ?? (col.rowActions ? "actions" : undefined);
  const spec = kind ? COLUMN_KINDS[kind] : undefined;
  const hasExplicitSize =
    col.size !== undefined ||
    col.minSize !== undefined ||
    col.maxSize !== undefined;
  // Pre-`kind` row-action columns keep their historical 2.5rem footprint.
  const legacyActions = col.rowActions === true && col.kind === undefined;
  const width = col.width ?? (legacyActions ? "2.5rem" : undefined);
  const explicitWidth = width !== undefined;
  const sized = !!spec || hasExplicitSize;

  const size = explicitWidth ? undefined : (col.size ?? spec?.size);
  const baseMin = col.minSize ?? (legacyActions ? 0 : (spec?.minSize ?? 0));
  // Header floor only widens kind-sized/explicitly sized columns, never legacy.
  const floor = sized ? headerFloor({ ...col, kind }) : 0;
  const minSize = Math.max(baseMin, kind === "select" ? 0 : floor);
  const rawMax = col.maxSize ?? spec?.maxSize;
  const maxSize = rawMax === undefined ? undefined : Math.max(rawMax, minSize);

  const grow = col.grow ?? (explicitWidth ? false : (spec?.grow ?? false));
  const overflow: OverflowPolicy = col.wrap
    ? "wrap"
    : (spec?.overflow ?? "legacy");

  return {
    kind,
    sized,
    size:
      size === undefined
        ? undefined
        : clamp(size, minSize, maxSize ?? Number.POSITIVE_INFINITY),
    minSize,
    maxSize,
    grow,
    align: col.align ?? spec?.align ?? "left",
    overflow,
    numeric: spec?.numeric === true,
    mono: spec?.mono === true,
    cssWidth: width,
    pin: col.pin === false ? undefined : (col.pin ?? spec?.pin),
  };
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max);
}

/**
 * Distribute `containerWidth` over the columns. Fixed columns take their
 * clamped preferred size; `grow` columns (and unsized legacy columns, which
 * keep today's equal share) split what remains, never below their minimum or
 * above their maximum.
 */
export function computeColumnWidths(
  layouts: ReadonlyArray<ResolvedColumnLayout>,
  containerWidth: number,
): number[] {
  const widths = layouts.map((layout) =>
    layout.grow || (layout.size === undefined && !layout.cssWidth)
      ? undefined
      : (layout.size ?? parsePx(layout.cssWidth) ?? layout.minSize),
  );
  const growIdx = widths.flatMap((w, i) => (w === undefined ? [i] : []));
  // When the preferred fixed sizes plus the flexible columns' minimums do not
  // fit, shrink the fixed columns proportionally toward their own minimums so
  // the grow column is never squeezed to nothing. Anything still over budget
  // overflows (scroll layout) rather than clipping the flexible column.
  const growMin = growIdx.reduce((sum, i) => sum + layouts[i].minSize, 0);
  const preferred = widths.reduce<number>((sum, w) => sum + (w ?? 0), 0);
  const deficit = preferred + growMin - containerWidth;
  if (deficit > 0) {
    const slack = widths.map((w, i) =>
      w === undefined ? 0 : Math.max(w - layouts[i].minSize, 0),
    );
    const slackTotal = slack.reduce((sum, v) => sum + v, 0);
    if (slackTotal > 0) {
      const cut = Math.min(deficit, slackTotal) / slackTotal;
      for (let i = 0; i < widths.length; i += 1) {
        const w = widths[i];
        if (w !== undefined) widths[i] = w - slack[i] * cut;
      }
    }
  }
  const fixedTotal = widths.reduce<number>((sum, w) => sum + (w ?? 0), 0);
  let remaining = Math.max(containerWidth - fixedTotal, 0);
  let open = growIdx.slice();
  const result = widths.map((w) => w ?? 0);
  // Iteratively settle columns that hit a bound so the rest share the rest.
  for (
    let guard = 0;
    guard < growIdx.length + 1 && open.length > 0;
    guard += 1
  ) {
    const share = remaining / open.length;
    const pinned = open.filter((i) => {
      const { minSize, maxSize } = layouts[i];
      return share < minSize || (maxSize !== undefined && share > maxSize);
    });
    if (pinned.length === 0) {
      for (const i of open) result[i] = share;
      open = [];
      break;
    }
    for (const i of pinned) {
      const { minSize, maxSize } = layouts[i];
      result[i] = clamp(share, minSize, maxSize ?? Number.POSITIVE_INFINITY);
      remaining -= result[i];
    }
    open = open.filter((i) => !pinned.includes(i));
    remaining = Math.max(remaining, 0);
  }
  return result;
}

/**
 * Smallest width at which a `fit` table can render without squeezing a
 * flexible column below its minimum: fixed columns at their preferred size
 * plus each grow column at its minSize. Undefined for fully legacy tables so
 * their historical equal-share layout is untouched. Narrower containers (a
 * phone) then scroll sideways instead of overlapping cells.
 */
export function minTableWidth(
  layouts: ReadonlyArray<ResolvedColumnLayout | undefined>,
  leadWidth = 0,
): number | undefined {
  if (!layouts.some((l) => l?.sized)) return undefined;
  const total = layouts.reduce((sum, l) => {
    if (!l) return sum;
    if (l.grow) return sum + l.minSize;
    return sum + (l.size ?? parsePx(l.cssWidth) ?? l.minSize);
  }, leadWidth);
  return Math.ceil(total);
}

export function parsePx(width: string | undefined): number | undefined {
  if (!width) return undefined;
  const match = /^(\d+(?:\.\d+)?)(px|rem)?$/.exec(width.trim());
  if (!match) return undefined;
  const value = Number(match[1]);
  return match[2] === "rem" ? value * 16 : value;
}

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

/** Inline sizing for a `<th>`/`<td>` in the semantic table. */
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
