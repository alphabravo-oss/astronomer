import { forwardRef, type ButtonHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

export interface SwitchProps extends Omit<
  ButtonHTMLAttributes<HTMLButtonElement>,
  "onChange"
> {
  checked?: boolean;
  onCheckedChange?: (checked: boolean) => void;
  /** Track/thumb size. `sm` is for dense rows (e.g. inline table cells). */
  size?: "sm" | "md";
}

const trackSizeClasses: Record<NonNullable<SwitchProps["size"]>, string> = {
  sm: "h-5 w-9",
  md: "h-6 w-11",
};

const thumbSizeClasses: Record<
  NonNullable<SwitchProps["size"]>,
  { base: string; checked: string; unchecked: string }
> = {
  sm: { base: "h-3.5 w-3.5", checked: "translate-x-4", unchecked: "translate-x-1" },
  md: { base: "h-4 w-4", checked: "translate-x-6", unchecked: "translate-x-1" },
};

export const Switch = forwardRef<HTMLButtonElement, SwitchProps>(
  (
    {
      checked = false,
      onCheckedChange,
      disabled,
      className,
      size = "md",
      ...props
    },
    ref,
  ) => (
    <button
      ref={ref}
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onCheckedChange?.(!checked)}
      className={cn(
        "relative inline-flex items-center rounded-full transition-colors",
        trackSizeClasses[size],
        checked ? "bg-status-success" : "bg-muted",
        disabled && "cursor-not-allowed opacity-60",
        className,
      )}
      {...props}
    >
      <span
        className={cn(
          "inline-block transform rounded-full bg-white transition-transform",
          thumbSizeClasses[size].base,
          checked
            ? thumbSizeClasses[size].checked
            : thumbSizeClasses[size].unchecked,
        )}
      />
    </button>
  ),
);

Switch.displayName = "Switch";
