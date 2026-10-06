/**
 * Column layout model for DataTable: semantic column kinds, sizing math,
 * ordering and pinning. Everything here is pure so it can be unit tested
 * without rendering.
 */

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
  // Fits "almost 2 years ago" at text-xs plus cell padding.
  age: {
    size: 140,
    minSize: 140,
    maxSize: 160,
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

export * from "@/components/ui/data-table-pinning";
