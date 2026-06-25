// §Schema / §DataProxy — Tier-1 declarative rendering logic (pure, no React).
//
// The host renders a declarative widget from (a) the manifest's render.declarative
// spec and (b) data fetched through the data proxy. NONE of this runs third-party
// JS: a proxy response is a JSON value, and these helpers map it to the rows /
// scalars / series the first-party renderers paint as TEXT NODES only.
//
// Keeping the spec->render mapping here (not inside the components) means it can
// be unit tested without rendering, and every formatter is a closed enum so a
// hostile manifest can never inject markup — the value always becomes a string.

import { formatBytes, formatDate } from '@/lib/utils';
import type {
  ChartSpec,
  DataShape,
  ExtensionDataResponse,
  FieldBinding,
  FieldFormat,
} from '@/lib/api/extensions';

// A single proxied row is an opaque JSON object; we only read declared dot-paths.
export type ProxyRow = Record<string, unknown>;

// ---------------------------------------------------------------------------
// JSONPath-lite: resolve a dot-path ("metadata.name") into a proxy row.
//
// Deliberately tiny — no wildcards, no array slices, no expressions. A path
// segment indexes either an object key or (when numeric) an array index. Any
// miss returns undefined rather than throwing, so a malformed manifest path
// degrades to an empty cell instead of crashing the widget.
// ---------------------------------------------------------------------------
export function getByPath(row: unknown, path: string): unknown {
  if (row == null || !path) return undefined;
  let cur: unknown = row;
  for (const seg of path.split('.')) {
    if (cur == null) return undefined;
    if (Array.isArray(cur)) {
      const idx = Number(seg);
      if (!Number.isInteger(idx) || idx < 0 || idx >= cur.length) return undefined;
      cur = cur[idx];
      continue;
    }
    if (typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

// ---------------------------------------------------------------------------
// Closed-enum value formatting. Every branch returns a plain string — the
// renderers place it in a text node, so there is no HTML-injection surface.
// `null`/`undefined`/non-finite numbers render as an em dash ("no data"),
// distinct from a real 0.
// ---------------------------------------------------------------------------
const EM_DASH = '—';

export function formatValue(value: unknown, format?: FieldFormat): string {
  if (value === null || value === undefined) return EM_DASH;

  switch (format) {
    case 'number':
      return formatNumberLike(value);
    case 'currency':
      return formatCurrency(value);
    case 'bytes': {
      const n = toFiniteNumber(value);
      return n === undefined ? String(value) : formatBytes(n);
    }
    case 'datetime': {
      const s = typeof value === 'string' || typeof value === 'number' ? String(value) : '';
      return s ? formatDate(s) : EM_DASH;
    }
    case 'duration':
      return formatDuration(value);
    case 'badge':
      // The badge label is the raw text; the renderer wraps it in a StatusBadge.
      return String(value);
    case 'text':
    default:
      return stringifyScalar(value);
  }
}

function toFiniteNumber(value: unknown): number | undefined {
  const n = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(n) ? n : undefined;
}

function formatNumberLike(value: unknown): string {
  const n = toFiniteNumber(value);
  if (n === undefined) return String(value);
  return n.toLocaleString('en-US', { maximumFractionDigits: 2 });
}

function formatCurrency(value: unknown): string {
  const n = toFiniteNumber(value);
  if (n === undefined) return String(value);
  return n.toLocaleString('en-US', { style: 'currency', currency: 'USD' });
}

// Duration: input is a count of seconds. Renders the largest two units
// (e.g. "1h 5m", "45s") so a numeric proxy field reads as a human duration.
export function formatDuration(value: unknown): string {
  const total = toFiniteNumber(value);
  if (total === undefined) return String(value);
  let secs = Math.max(0, Math.floor(total));
  const d = Math.floor(secs / 86400);
  secs -= d * 86400;
  const h = Math.floor(secs / 3600);
  secs -= h * 3600;
  const m = Math.floor(secs / 60);
  secs -= m * 60;
  const parts: string[] = [];
  if (d) parts.push(`${d}d`);
  if (h) parts.push(`${h}h`);
  if (m) parts.push(`${m}m`);
  if (secs || parts.length === 0) parts.push(`${secs}s`);
  return parts.slice(0, 2).join(' ');
}

// A scalar that is not run through a numeric/date formatter: objects/arrays are
// JSON-stringified so a cell never renders "[object Object]" or leaks a nested
// structure as live nodes. Strings/numbers/booleans pass through as text.
function stringifyScalar(value: unknown): string {
  if (typeof value === 'object') {
    try {
      return JSON.stringify(value);
    } catch {
      return EM_DASH;
    }
  }
  return String(value);
}

// ---------------------------------------------------------------------------
// Response shaping: the proxy returns { data, shape, meta }. A list widget wants
// an array of rows; an object widget (stat) wants a single row; a series widget
// (chart) wants an array of points. This normalizes any of the shapes the proxy
// can return into the row array the renderers iterate, tolerating the data being
// either the bare value or wrapped in { rows }.
// ---------------------------------------------------------------------------
export function extractRows(res: ExtensionDataResponse | undefined): ProxyRow[] {
  if (!res) return [];
  const data = res.data as unknown;
  const unwrapped = unwrapRows(data);

  if (Array.isArray(unwrapped)) {
    return unwrapped.filter(isRecord);
  }
  if (isRecord(unwrapped)) {
    return [unwrapped];
  }
  return [];
}

// The single row for an object/stat widget: first extracted row, or {} so a
// stat with a missing field renders an em dash rather than crashing.
export function extractObject(res: ExtensionDataResponse | undefined): ProxyRow {
  return extractRows(res)[0] ?? {};
}

function unwrapRows(data: unknown): unknown {
  if (isRecord(data) && Array.isArray((data as { rows?: unknown }).rows)) {
    return (data as { rows: unknown[] }).rows;
  }
  return data;
}

function isRecord(v: unknown): v is ProxyRow {
  return typeof v === 'object' && v !== null && !Array.isArray(v);
}

// ---------------------------------------------------------------------------
// Chart series projection: ChartSpec names an x field and N y fields. We turn
// rows into { x, y[] } points, coercing y values to finite numbers (non-numeric
// -> 0 so a bar/line still has a defined height). Pure so the chart geometry is
// unit-testable independent of SVG.
// ---------------------------------------------------------------------------
export interface ChartPoint {
  x: string;
  values: number[];
}

export function buildChartSeries(rows: ProxyRow[], spec: ChartSpec): ChartPoint[] {
  return rows.map((row) => ({
    x: formatValue(getByPath(row, spec.x), 'text'),
    values: spec.y.map((yPath) => toFiniteNumber(getByPath(row, yPath)) ?? 0),
  }));
}

// Max across every y value of every point — the denominator a renderer uses to
// scale bar/area heights. Always >= 0; 0 when there is no positive value.
export function chartMax(points: ChartPoint[]): number {
  let max = 0;
  for (const p of points) {
    for (const v of p.values) {
      if (v > max) max = v;
    }
  }
  return max;
}

// Whether a fetched response carries any renderable data for the given shape.
// Drives the empty-state branch uniformly across the renderers.
export function isEmptyResponse(
  res: ExtensionDataResponse | undefined,
  shape: DataShape,
): boolean {
  if (!res) return true;
  const rows = extractRows(res);
  if (shape === 'object') {
    return Object.keys(rows[0] ?? {}).length === 0;
  }
  return rows.length === 0;
}

// Column projection for a table: explicit field bindings, or — when a manifest
// omits fields — the union of keys across the first row, each as a text column.
// (The proxy already projected to the allowlisted fields server-side; this is a
// display fallback so a fields-less table still renders something sane.)
export function tableColumns(rows: ProxyRow[], fields?: FieldBinding[]): FieldBinding[] {
  if (fields && fields.length) return fields;
  const first = rows[0];
  if (!first) return [];
  return Object.keys(first).map((k) => ({ path: k, label: k, format: 'text' as FieldFormat }));
}
