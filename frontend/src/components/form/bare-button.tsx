import { forwardRef } from "react";
import {
  ActionButton,
  type ActionButtonProps,
} from "@/components/ui/action-button";
import { cn } from "@/lib/utils";

/**
 * ActionButton with no visual preset (intent="bare", size="none"): a custom
 * clickable surface (card, row, link-like control) that supplies its own
 * classes but still gets the shared tooltip, loading and a11y behaviour.
 * The leading classes undo ActionButton's base layout so the surface renders
 * like the native button it replaces; callers override any of them.
 */
export const BareButton = forwardRef<HTMLButtonElement, ActionButtonProps>(
  ({ className, ...props }, ref) => (
    <ActionButton
      ref={ref}
      intent="bare"
      size="none"
      className={cn(
        "inline-block shrink items-stretch justify-start gap-0 whitespace-normal rounded-none font-normal",
        className,
      )}
      {...props}
    />
  ),
);

BareButton.displayName = "BareButton";
