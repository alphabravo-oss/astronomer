import { forwardRef, type SelectHTMLAttributes } from "react";
import { cn } from "@/lib/utils";
import { controlClassName } from "@/components/ui/input";
import { ChevronDown } from "lucide-react";

export type SelectProps = SelectHTMLAttributes<HTMLSelectElement> & {
  /** Layout belongs to the wrapper; visual control styles belong to className. */
  containerClassName?: string;
};

export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, containerClassName, children, ...props }, ref) => (
    <span className={cn("relative block w-full", containerClassName)}>
      <select
        ref={ref}
        className={cn(controlClassName, "appearance-none pr-9", className)}
        {...props}
      >
        {children}
      </select>
      <ChevronDown
        aria-hidden="true"
        className="pointer-events-none absolute right-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
      />
    </span>
  ),
);

Select.displayName = "Select";
