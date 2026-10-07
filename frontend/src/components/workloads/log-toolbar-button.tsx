import type { ReactNode } from "react";
import { cn } from "@/lib/utils";
import { BareButton } from "@/components/form/bare-button";

/** Icon toggle used by the pod log toolbars (inline viewer and console tab). */
export function LogToolbarButton({
  tooltip,
  label,
  pressed,
  activeClass = "bg-accent text-foreground",
  compact = false,
  disabled,
  disabledReason,
  onClick,
  children,
}: {
  tooltip?: string;
  label: string;
  pressed?: boolean;
  activeClass?: string;
  compact?: boolean;
  disabled?: boolean;
  disabledReason?: string;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <BareButton
      tooltip={tooltip}
      disabledReason={disabledReason}
      onClick={onClick}
      disabled={disabled}
      aria-label={label}
      aria-pressed={pressed}
      className={cn(
        "inline-flex items-center gap-1 rounded-sm font-normal transition-colors",
        compact ? "h-6 px-1.5 text-2xs" : "h-7 px-2 text-xs",
        pressed
          ? activeClass
          : "text-muted-foreground hover:bg-accent hover:text-foreground",
        disabled && "cursor-not-allowed opacity-40",
      )}
    >
      {children}
    </BareButton>
  );
}
