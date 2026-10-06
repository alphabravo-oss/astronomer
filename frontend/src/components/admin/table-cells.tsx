import type { ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { parseISO } from "date-fns";
import { cn, formatDate } from "@/lib/utils";

/** Split `items` into the first `max` and the overflow count. */
export function capChips<T>(
  items: readonly T[],
  max = 2,
): { visible: T[]; hidden: T[] } {
  return { visible: items.slice(0, max), hidden: items.slice(max) };
}

const CHIP =
  "inline-flex max-w-full items-center rounded-sm px-2 py-0.5 text-xs whitespace-nowrap";

/**
 * Chips capped at `max` (default 2) with a "+N" overflow chip whose Tooltip
 * lists the rest. Stays on one line so the row height does not change.
 */
export function CappedChips({
  items,
  max = 2,
  mono,
  empty = "—",
  renderChip,
}: {
  items: readonly string[];
  max?: number;
  mono?: boolean;
  empty?: ReactNode;
  renderChip?: (item: string) => ReactNode;
}) {
  if (items.length === 0) {
    return <span className="text-xs text-muted-foreground">{empty}</span>;
  }
  const { visible, hidden } = capChips(items, max);
  return (
    <div className="flex min-w-0 items-center gap-1 overflow-hidden">
      {visible.map((item) =>
        renderChip ? (
          <span key={item} className="min-w-0 shrink">
            {renderChip(item)}
          </span>
        ) : (
          <span
            key={item}
            className={cn(
              CHIP,
              "min-w-0 shrink truncate bg-muted text-muted-foreground",
              mono && "font-mono",
            )}
          >
            {item}
          </span>
        ),
      )}
      {hidden.length > 0 && (
        <Tooltip content={hidden.join(", ")}>
          <span
            className={cn(CHIP, "shrink-0 bg-muted text-muted-foreground")}
            aria-label={`${hidden.length} more: ${hidden.join(", ")}`}
          >
            +{hidden.length}
          </span>
        </Tooltip>
      )}
    </div>
  );
}

/**
 * Compact relative age for narrow age columns: `45s`, `12m`, `3h`, `8d`,
 * `5mo`, `2y`, with an `ago` suffix (or `in` prefix for future times).
 * Returns undefined for missing, unparsable or Go-zero timestamps.
 */
export function shortRelative(
  value: string | null | undefined,
  now: number = Date.now(),
): string | undefined {
  if (!value) return undefined;
  const date = parseISO(value);
  const time = date.getTime();
  if (Number.isNaN(time) || date.getUTCFullYear() <= 1 || time === 0) {
    return undefined;
  }
  const delta = Math.round((now - time) / 1000);
  const abs = Math.abs(delta);
  let text: string;
  if (abs < 45) text = "now";
  else if (abs < 3600) text = `${Math.max(1, Math.round(abs / 60))}m`;
  else if (abs < 86400) text = `${Math.round(abs / 3600)}h`;
  else if (abs < 86400 * 30) text = `${Math.round(abs / 86400)}d`;
  else if (abs < 86400 * 365) text = `${Math.round(abs / (86400 * 30))}mo`;
  else text = `${Math.round(abs / (86400 * 365))}y`;
  if (text === "now") return "just now";
  return delta >= 0 ? `${text} ago` : `in ${text}`;
}

/** Compact relative age ("3h ago") with the exact timestamp in a Tooltip. */
export function RelativeTime({
  value,
  fallback,
}: {
  value: string | null | undefined;
  fallback?: ReactNode;
}) {
  const short = shortRelative(value);
  if (!value || short === undefined) {
    return <span className="text-muted-foreground">{fallback ?? "Never"}</span>;
  }
  return (
    <Tooltip content={formatDate(value)}>
      <span className="whitespace-nowrap">{short}</span>
    </Tooltip>
  );
}

/** Two stacked lines: primary value over a muted secondary line. */
export function TwoLine({
  primary,
  secondary,
  mono,
}: {
  primary: ReactNode;
  secondary?: ReactNode;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0 leading-tight">
      <div className={cn("truncate", mono && "font-mono text-xs")}>
        {primary}
      </div>
      {secondary != null && secondary !== "" && (
        <div className="mt-0.5 truncate text-xs text-muted-foreground">
          {secondary}
        </div>
      )}
    </div>
  );
}
