import type { ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { AgeCell } from "@/components/ui/age-cell";
import { StackedCell } from "@/components/ui/stacked-cell";
import { cn } from "@/lib/utils";

/**
 * Small cell building blocks shared by table column definitions: capped chip
 * lists, relative timestamps with an exact tooltip, name + subtitle cells and
 * fixed-precision numbers.
 */

const CHIP_CLASS =
  "min-w-0 max-w-32 truncate rounded-sm bg-muted px-2 py-0.5 text-xs text-muted-foreground";

/**
 * Chips capped at `max` plus a "+N" overflow chip whose Tooltip lists the
 * hidden entries. Chips never wrap, so the row height stays constant.
 */
export function CappedChips({
  items,
  max = 2,
  mono = true,
  empty = "—",
}: {
  items: readonly string[] | null | undefined;
  max?: number;
  mono?: boolean;
  empty?: ReactNode;
}) {
  if (!items || items.length === 0) {
    return <span className="text-xs text-muted-foreground">{empty}</span>;
  }
  const shown = items.slice(0, max);
  const hidden = items.slice(max);
  return (
    <div className="flex min-w-0 items-center gap-1 overflow-hidden">
      {shown.map((item, i) => (
        <Tooltip key={`${item}-${i}`} content={item}>
          <span className={cn(CHIP_CLASS, mono && "font-mono")}>{item}</span>
        </Tooltip>
      ))}
      {hidden.length > 0 && (
        <Tooltip
          wrap
          content={
            <ul className="space-y-0.5 font-mono">
              {hidden.map((item, i) => (
                <li key={`${item}-${i}`}>{item}</li>
              ))}
            </ul>
          }
        >
          <span
            aria-label={`${hidden.length} more: ${hidden.join(", ")}`}
            className={cn(CHIP_CLASS, "shrink-0 tabular-nums")}
          >
            +{hidden.length}
          </span>
        </Tooltip>
      )}
    </div>
  );
}

/** Compact relative age (AgeCell) with an optional leading word such as "Started". */
export function TimestampCell({
  value,
  prefix,
}: {
  value: string | null | undefined;
  prefix?: string;
}) {
  if (!prefix || !value) return <AgeCell value={value} empty="Never" />;
  return (
    <span className="inline-flex gap-1">
      <span className="text-xs text-muted-foreground">{prefix}</span>
      <AgeCell value={value} />
    </span>
  );
}

/** Primary label with a muted subtitle (StackedCell), optionally led by an icon. */
export function NameSubCell({
  title,
  subtitle,
  icon,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  icon?: ReactNode;
}) {
  return (
    <div className="flex min-w-0 items-center gap-2">
      {icon ? <span className="shrink-0">{icon}</span> : null}
      <div className="min-w-0 flex-1">
        <StackedCell primary={title} secondary={subtitle} />
      </div>
    </div>
  );
}

/** Fixed-precision float; non-finite values render as an em dash. */
export function formatFixed(
  value: number | null | undefined,
  digits = 2,
): string {
  return value == null || !Number.isFinite(value) ? "—" : value.toFixed(digits);
}

/** Right-aligned, tabular, monospaced numeric cell (alignment comes from the column kind). */
export function NumberCell({
  value,
  digits = 2,
  className,
}: {
  value: number | null | undefined;
  digits?: number;
  className?: string;
}) {
  return (
    <span className={cn("font-mono text-xs tabular-nums", className)}>
      {formatFixed(value, digits)}
    </span>
  );
}
