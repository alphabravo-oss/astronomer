import { useState, type ReactNode } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";

/** One truncating line whose full text appears in a Tooltip only when clipped. */
function ClipLine({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  const [full, setFull] = useState<string | undefined>();
  return (
    <Tooltip content={full}>
      <div
        onPointerEnter={(event) => {
          const el = event.currentTarget;
          setFull(
            el.scrollWidth > el.clientWidth + 1
              ? (el.textContent ?? undefined)
              : undefined,
          );
        }}
        className={cn("min-w-0 truncate", className)}
      >
        {children}
      </div>
    </Tooltip>
  );
}

/**
 * Two-line cell: a primary value over a muted secondary value. Each line
 * truncates on its own and exposes the full text through a Tooltip.
 */
export function StackedCell({
  primary,
  secondary,
  primaryClassName,
  secondaryMono = false,
}: {
  primary: ReactNode;
  secondary?: ReactNode;
  primaryClassName?: string;
  secondaryMono?: boolean;
}) {
  return (
    <div className="min-w-0">
      <ClipLine className={cn("font-medium text-foreground", primaryClassName)}>
        {primary}
      </ClipLine>
      {secondary !== undefined && secondary !== null && secondary !== "" ? (
        <ClipLine
          className={cn(
            "text-xs text-muted-foreground",
            secondaryMono && "font-mono",
          )}
        >
          {secondary}
        </ClipLine>
      ) : null}
    </div>
  );
}
