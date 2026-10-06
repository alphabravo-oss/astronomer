import type { ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { cn, formatDate, formatRelativeTime } from "@/lib/utils";

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

/** Relative age ("3 hours ago") with the exact timestamp in a Tooltip. */
export function RelativeTime({
  value,
  fallback,
}: {
  value: string | null | undefined;
  fallback?: ReactNode;
}) {
  if (!value || formatRelativeTime(value) === "Never") {
    return <span className="text-muted-foreground">{fallback ?? "Never"}</span>;
  }
  return (
    <Tooltip content={formatDate(value)}>
      <span className="whitespace-nowrap">{formatRelativeTime(value)}</span>
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
