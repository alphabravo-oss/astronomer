import { forwardRef, type InputHTMLAttributes } from "react";
import { Check } from "lucide-react";

import { cn } from "@/lib/utils";

export type CheckboxProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type">;

/**
 * Theme-aware native checkbox.
 *
 * The real input remains the interactive and accessible element while the
 * sibling elements provide consistent light/dark rendering across browsers.
 */
export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(
  ({ className, disabled, ...props }, ref) => (
    <span className="relative inline-flex h-4 w-4 shrink-0 align-middle">
      <input
        ref={ref}
        type="checkbox"
        disabled={disabled}
        className={cn(
          "peer absolute inset-0 z-10 h-4 w-4 cursor-pointer appearance-none rounded-sm opacity-0",
          "disabled:cursor-not-allowed",
          className,
        )}
        {...props}
      />
      <span
        aria-hidden="true"
        data-slot="checkbox-indicator"
        className="pointer-events-none absolute inset-0 rounded-sm border border-input bg-background transition-colors peer-checked:border-primary peer-checked:bg-primary peer-focus-visible:ring-2 peer-focus-visible:ring-ring peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-background peer-disabled:opacity-50"
      />
      <Check
        aria-hidden="true"
        strokeWidth={3}
        className="pointer-events-none absolute inset-0 h-4 w-4 p-0.5 text-primary-foreground opacity-0 transition-opacity peer-checked:opacity-100 peer-disabled:opacity-50"
      />
    </span>
  ),
);

Checkbox.displayName = "Checkbox";
