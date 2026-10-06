/**
 * Shared cell builders for the networking / storage / policy / RBAC resource
 * tables (plan 031 P6b). They pair with the DataTable column `kind` so cells
 * never clip silently: long values get a tooltip, chip lists are capped, and
 * relative ages expose the exact timestamp.
 */
import { Tooltip } from "@/components/ui/tooltip";
import type { Column } from "@/components/ui/data-table";
import { formatDate, formatRelativeTime } from "@/lib/utils";

const CHIP_CLASS =
  "px-1.5 py-0.5 rounded-sm text-2xs bg-muted text-muted-foreground whitespace-nowrap";
const LONG_CHIP = 22;

function Chip({
  text,
  mono,
  className,
}: {
  text: string;
  mono: boolean;
  className?: string;
}) {
  return (
    <span
      className={`${CHIP_CLASS} ${text.length <= 14 ? "shrink-0" : "min-w-0 max-w-44 truncate"} ${mono ? "font-mono" : ""} ${className ?? ""}`}
    >
      {text}
    </span>
  );
}

/**
 * Label / selector / listener chips capped at `max`, with a "+N" chip. The
 * full list is exposed through a tooltip whenever anything is hidden or cut.
 */
export function ChipList({
  items,
  max = 2,
  mono = true,
  empty = "-",
}: {
  items: readonly string[] | null | undefined;
  max?: number;
  mono?: boolean;
  empty?: string;
}) {
  if (!items?.length) {
    return <span className="text-xs text-muted-foreground">{empty}</span>;
  }
  const shown = items.slice(0, max);
  const hidden = items.length - shown.length;
  const needsTip = hidden > 0 || items.some((s) => s.length > LONG_CHIP);
  return (
    <Tooltip
      content={
        needsTip ? (
          <ul className="space-y-0.5 font-mono">
            {items.map((item, i) => (
              <li key={`${item}-${i}`}>{item}</li>
            ))}
          </ul>
        ) : undefined
      }
    >
      <div data-cell-clip="" className="flex min-w-0 items-center gap-1">
        {shown.map((item, i) => (
          <Chip key={`${item}-${i}`} text={item} mono={mono} />
        ))}
        {hidden > 0 && (
          <span className={`${CHIP_CLASS} shrink-0 tabular-nums`}>
            +{hidden}
          </span>
        )}
      </div>
    </Tooltip>
  );
}

/** Relative age with the exact timestamp in a tooltip. */
export function AgeCell({
  iso,
  empty,
}: {
  iso?: string | null;
  empty?: string;
}) {
  const text = !iso && empty ? empty : formatRelativeTime(iso);
  const exact = iso && text !== "Never" ? formatDate(iso) : undefined;
  return (
    <Tooltip content={exact}>
      <span className="text-xs text-muted-foreground">{text}</span>
    </Tooltip>
  );
}

/** Standard "Age" column (kind `age`) sorted chronologically. */
export function ageColumn<T extends { createdAt?: string | null }>(
  empty?: string,
): Column<T> {
  return {
    key: "age",
    header: "Age",
    kind: "age",
    // "almost 2 years ago" must stay on one line.
    size: 140,
    minSize: 140,
    accessor: (row) => <AgeCell iso={row.createdAt} empty={empty} />,
    sortAccessor: (row) => row.createdAt ?? "",
    searchAccessor: (row) =>
      row.createdAt ? formatRelativeTime(row.createdAt) : (empty ?? ""),
  };
}

/** Standard "Namespace" column: mono text, room for `kube-system`-length names. */
export function namespaceColumn<T extends { namespace?: string | null }>(
  fallback = "",
): Column<T> {
  return {
    key: "namespace",
    header: "Namespace",
    kind: "text",
    minSize: 120,
    size: 128,
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {row.namespace || fallback}
      </span>
    ),
    sortAccessor: (row) => row.namespace ?? "",
  };
}

/** Right-aligned numeric column (kind `count`). */
export function countColumn<T>(
  key: string,
  header: string,
  value: (row: T) => number,
  extra?: Partial<Column<T>>,
): Column<T> {
  return {
    key,
    header,
    kind: "count",
    accessor: (row) => <span className="text-xs">{value(row)}</span>,
    sortAccessor: value,
    ...extra,
  };
}

/** Mono, one-line-then-tooltip text column (kind `text`). */
export function monoTextColumn<T>(
  key: string,
  header: string,
  value: (row: T) => string,
  extra?: Partial<Column<T>>,
): Column<T> {
  return {
    key,
    header,
    kind: "text",
    accessor: (row) => (
      <span className="text-xs text-muted-foreground font-mono">
        {value(row) || "-"}
      </span>
    ),
    sortAccessor: value,
    ...extra,
  };
}

/** Wrap `nameColumn()` output with the grow-on-name sizing kind. */
export function withNameKind<T>(column: Column<T>, minSize = 160): Column<T> {
  return { ...column, kind: "name", minSize };
}

/**
 * Name column used as the table's static definition; live tables replace it
 * with a drill-down link via `nameColumn()` + `withNameKind()`.
 */
export function nameStubColumn<T extends { name: string }>(
  minSize = 160,
): Column<T> {
  return {
    key: "name",
    header: "Name",
    kind: "name",
    minSize,
    accessor: (row) => (
      <span className="font-medium text-foreground font-mono text-xs">
        {row.name}
      </span>
    ),
    sortAccessor: (row) => row.name,
  };
}

/** Shared static part of the trailing row-actions column. */
export const ACTIONS_COLUMN = {
  key: "actions",
  header: "",
  rowActions: true,
  kind: "actions",
} as const;
