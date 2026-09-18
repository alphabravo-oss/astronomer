import { forwardRef, type InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils";

/** Shared control chrome — inputs, selects, and textareas use this string. */
export const controlClassName =
  "flex h-9 w-full rounded-md border border-input bg-background px-3 text-sm text-foreground placeholder:text-muted-foreground focus:outline-hidden focus:ring-2 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-60";

export type InputProps = InputHTMLAttributes<HTMLInputElement>;

export const Input = forwardRef<HTMLInputElement, InputProps>(
  ({ className, type = "text", ...props }, ref) => (
    <input
      ref={ref}
      type={type}
      className={cn(
        type === "checkbox" || type === "radio"
          ? "h-4 w-4 shrink-0 border-input accent-primary focus:ring-2 focus:ring-ring disabled:cursor-not-allowed disabled:opacity-60"
          : controlClassName,
        className,
      )}
      {...props}
    />
  ),
);

Input.displayName = "Input";
