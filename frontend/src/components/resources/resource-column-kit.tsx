import { Tooltip } from "@/components/ui/tooltip";
import type { Column } from "@/components/ui/data-table";
import { cn, formatDate, formatRelativeTime } from "@/lib/utils";

/** Pixel floor for `n` characters of 12px text plus cell padding. */
export const chWidth = (n: number) => Math.round(n * 7) + 24;

const MUTED = "text-xs text-muted-foreground";

/** Exact local timestamp for tooltips; undefined for absent or zero dates. */
export function exactTimestamp(value?: string | null): string | undefined {
  if (!value) return undefined;
  const t = Date.parse(value);
  if (Number.isNaN(t) || new Date(t).getUTCFullYear() <= 1) return undefined;
  return formatDate(value);
}

/** Relative timestamp whose exact time is revealed on hover/focus. */
export function AgeCell({ value }: { value?: string | null }) {
  const text = formatRelativeTime(value);
  const exact = exactTimestamp(value);
  if (!exact) return <span className={MUTED}>{text}</span>;
  return (
    <Tooltip content={exact}>
      <span className={MUTED}>{text}</span>
    </Tooltip>
  );
}

/** Up to `max` chips followed by a "+N" chip whose tooltip lists everything. */
export function ChipList({
  items,
  max = 2,
  mono = true,
  empty = "-",
}: {
  items?: readonly string[] | null;
  max?: number;
  mono?: boolean;
  empty?: string;
}) {
  const list = items ?? [];
  if (list.length === 0) return <span className={MUTED}>{empty}</span>;
  const shown = list.slice(0, max);
  const rest = list.length - shown.length;
  return (
    <div className="flex min-w-0 items-center gap-1">
      {shown.map((item, i) => (
        <Tooltip key={`${item}-${i}`} content={item.length > 14 ? item : null}>
          <span
            className={cn(
              "min-w-0 truncate rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground",
              mono && "font-mono",
            )}
          >
            {item}
          </span>
        </Tooltip>
      ))}
      {rest > 0 && (
        <Tooltip
          wrap
          content={
            <ul className="space-y-0.5 font-mono">
              {list.map((item, i) => (
                <li key={`${item}-${i}`}>{item}</li>
              ))}
            </ul>
          }
        >
          <span className="shrink-0 rounded-sm bg-muted px-1.5 py-0.5 text-2xs text-muted-foreground">
            +{rest}
          </span>
        </Tooltip>
      )}
    </div>
  );
}

const ACCESS_MODES: Record<string, string> = {
  ReadWriteOnce: "RWO",
  ReadOnlyMany: "ROX",
  ReadWriteMany: "RWX",
  ReadWriteOncePod: "RWOP",
};

export function abbreviateAccessModes(modes?: readonly string[] | null) {
  return (modes ?? []).map((m) => ACCESS_MODES[m] ?? m);
}

/** `RWO, RWX` with the long names in a tooltip. */
export function AccessModesCell({
  modes,
}: {
  modes?: readonly string[] | null;
}) {
  const list = modes ?? [];
  if (list.length === 0) return <span className={MUTED}>-</span>;
  return (
    <Tooltip content={list.join(", ")}>
      <span className={MUTED}>{abbreviateAccessModes(list).join(", ")}</span>
    </Tooltip>
  );
}

type Opts<T> = Partial<Omit<Column<T>, "key" | "header" | "accessor">>;

/** Single-line text/mono value with a fallback; searchable and CSV-exportable. */
export function textColumn<T>(
  key: string,
  header: string,
  get: (row: T) => string | null | undefined,
  opts: Opts<T> & { mono?: boolean; fallback?: string } = {},
): Column<T> {
  const { mono = false, fallback = "-", ...rest } = opts;
  const value = (row: T) => get(row) || fallback;
  return {
    key,
    header,
    kind: "text",
    accessor: (row) => (
      <span className={cn(MUTED, mono && "font-mono")}>{value(row)}</span>
    ),
    searchAccessor: value,
    ...rest,
  };
}

/** Right-aligned numeric column (count kind). */
export function countColumn<T>(
  key: string,
  header: string,
  get: (row: T) => number | string | null | undefined,
  opts: Opts<T> = {},
): Column<T> {
  const value = (row: T) => String(get(row) ?? 0);
  return {
    key,
    header,
    kind: "count",
    accessor: (row) => <span className="text-xs">{value(row)}</span>,
    searchAccessor: value,
    ...opts,
  };
}

/** Relative age with an exact-timestamp tooltip. */
export function ageColumn<T>(
  get: (row: T) => string | null | undefined,
  opts: Opts<T> & { key?: string; header?: string } = {},
): Column<T> {
  const { key = "age", header = "Age", ...rest } = opts;
  return {
    key,
    header,
    kind: "age",
    size: 120,
    minSize: 112,
    accessor: (row) => <AgeCell value={get(row)} />,
    searchAccessor: (row) => formatRelativeTime(get(row)),
    ...rest,
  };
}

/** Chip list capped at two plus "+N". */
export function chipColumn<T>(
  key: string,
  header: string,
  get: (row: T) => readonly string[] | null | undefined,
  opts: Opts<T> & { mono?: boolean; max?: number } = {},
): Column<T> {
  const { mono = true, max = 2, ...rest } = opts;
  return {
    key,
    header,
    kind: "badge",
    accessor: (row) => <ChipList items={get(row)} mono={mono} max={max} />,
    searchAccessor: (row) => (get(row) ?? []).join(" "),
    sortable: false,
    ...rest,
  };
}

/** Resource name (mono); the only growing column in resource tables. */
export function plainNameColumn<T extends { name: string }>(): Column<T> {
  return {
    key: "name",
    header: "Name",
    kind: "name",
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
    searchAccessor: (row) => row.name,
  };
}

export function namespaceColumn<T extends { namespace?: string }>(
  opts: Opts<T> = {},
): Column<T> {
  return textColumn<T>("namespace", "Namespace", (row) => row.namespace, {
    mono: true,
    size: 124,
    minSize: 112,
    ...opts,
  });
}
