import type { ReactElement, ReactNode } from "react";
import { Tooltip as TooltipPrimitive } from "radix-ui";
import { cn } from "@/lib/utils";

export interface TooltipProps {
  /** Tooltip content. Falsy content shows nothing but keeps the tree stable so the trigger never remounts. */
  content: ReactNode;
  children: ReactElement;
  side?: "top" | "right" | "bottom" | "left";
  /**
   * Wrap the trigger in a focusable span. Required for disabled buttons, which
   * emit no pointer events, so the reason a control is disabled stays visible.
   */
  wrap?: boolean;
  className?: string;
}

export function Tooltip({
  content,
  children,
  side = "top",
  wrap = false,
  className,
}: TooltipProps) {
  // ponytail: provider per tooltip so components render in tests and portals
  // without an app-root provider; cost is losing the cross-tooltip skip delay.
  return (
    <TooltipPrimitive.Provider delayDuration={300}>
      <TooltipPrimitive.Root>
        <TooltipPrimitive.Trigger asChild>
          {wrap ? (
            // Disabled buttons are not focusable; the wrapper keeps the reason reachable.
            // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex
            <span tabIndex={0} className="inline-flex">
              {children}
            </span>
          ) : (
            children
          )}
        </TooltipPrimitive.Trigger>
        {content ? (
          <TooltipPrimitive.Portal>
            <TooltipPrimitive.Content
              side={side}
              sideOffset={6}
              collisionPadding={8}
              className={cn(
                "z-toast max-w-xs rounded-md border border-border bg-popover px-2.5 py-1.5 text-xs text-popover-foreground shadow-md",
                className,
              )}
            >
              {content}
            </TooltipPrimitive.Content>
          </TooltipPrimitive.Portal>
        ) : null}
      </TooltipPrimitive.Root>
    </TooltipPrimitive.Provider>
  );
}
