import { useState, type ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { toastSuccess } from "@/lib/toast";
import { cn } from "@/lib/utils";
import type { OverflowPolicy } from "@/components/ui/data-table-layout";

/** Keep the head and tail of an identifier: `3f9a2c1e…b7d4`. */
export function middleEllipsis(text: string, maxChars: number): string {
  if (text.length <= maxChars || maxChars < 8) return text;
  const tail = Math.min(5, Math.floor(maxChars / 3));
  const head = maxChars - tail - 1;
  return `${text.slice(0, head)}…${text.slice(-tail)}`;
}

/** Approximate characters of a 12px monospace id that fit in `widthPx`. */
export function idCharBudget(widthPx: number | undefined): number {
  return Math.max(8, Math.floor(((widthPx ?? 160) - 28) / 7));
}

/**
 * A cell value that clips visually but always exposes the full value through
 * the Tooltip primitive. Clipping is measured on hover/focus so unclipped
 * values never show a redundant tooltip, and nothing relies on `title`.
 */
function ClippedValue({
  children,
  clampLines,
  className,
}: {
  children: ReactNode;
  clampLines?: 2;
  className?: string;
}) {
  const [full, setFull] = useState<string | undefined>();
  const measure = (el: HTMLElement) => {
    const clipped =
      el.scrollWidth > el.clientWidth + 1 ||
      el.scrollHeight > el.clientHeight + 1;
    setFull(clipped ? (el.textContent ?? undefined) : undefined);
  };
  return (
    <Tooltip content={full}>
      <div
        data-cell-clip=""
        onPointerEnter={(event) => measure(event.currentTarget)}
        className={cn(
          "min-w-0 overflow-hidden",
          clampLines
            ? "line-clamp-2 whitespace-normal break-words"
            : "truncate",
          className,
        )}
      >
        {children}
      </div>
    </Tooltip>
  );
}

function copyText(text: string) {
  void navigator.clipboard
    ?.writeText(text)
    .then(() => toastSuccess("Copied to clipboard"))
    .catch(() => undefined);
}

export function DataTableCellContent({
  overflow,
  mono,
  numeric,
  label,
  text,
  width,
  children,
}: {
  overflow: OverflowPolicy;
  mono: boolean;
  numeric: boolean;
  /** Column header, used for the copy button's accessible name. */
  label: string;
  /** Plain-text value (search/sort text); used by the id kind. */
  text: string;
  width: number | undefined;
  children: ReactNode;
}) {
  const numberClass = numeric && "tabular-nums";
  switch (overflow) {
    case "fixed":
      return <div className="min-w-0 overflow-visible">{children}</div>;
    case "truncate":
      return (
        <ClippedValue className={cn(numberClass)}>{children}</ClippedValue>
      );
    case "wrap-2":
      return <ClippedValue clampLines={2}>{children}</ClippedValue>;
    case "nowrap":
      return (
        <div className={cn("min-w-0 whitespace-nowrap", numberClass)}>
          {children}
        </div>
      );
    case "middle": {
      if (!text) {
        return (
          <div className={cn("min-w-0 whitespace-nowrap", mono && "font-mono")}>
            {children}
          </div>
        );
      }
      return (
        <Tooltip content={`${text} (click to copy)`}>
          <button
            type="button"
            aria-label={`Copy ${label || "value"} ${text}`}
            onClick={(event) => {
              event.stopPropagation();
              copyText(text);
            }}
            className="max-w-full cursor-copy whitespace-nowrap rounded-sm font-mono text-xs hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
          >
            {middleEllipsis(text, idCharBudget(width))}
          </button>
        </Tooltip>
      );
    }
    case "wrap":
      return (
        <div className="min-w-0 overflow-hidden whitespace-normal break-words">
          {children}
        </div>
      );
    default:
      // legacy: exactly the pre-`kind` markup.
      return (
        <div className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
          {children}
        </div>
      );
  }
}
