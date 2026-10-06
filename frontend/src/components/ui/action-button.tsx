import { forwardRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";
import { Tooltip } from "@/components/ui/tooltip";

type ActionIntent =
  | "default"
  | "primary"
  | "destructive"
  | "ghost"
  | "link"
  /** Behavior only: no visual classes. For custom clickable surfaces (cards, rows). */
  | "bare";
type ActionSize = "xs" | "sm" | "md" | "icon" | "icon-xs" | "none";

export interface ActionButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  intent?: ActionIntent;
  size?: ActionSize;
  icon?: ReactNode;
  loading?: boolean;
  loadingLabel?: ReactNode;
  disabledReason?: string;
  /** Styled tooltip; `title` is kept as an alias so existing call sites migrate for free. */
  tooltip?: ReactNode;
}

const intentClass: Record<ActionIntent, string> = {
  default: "border border-border bg-background text-foreground hover:bg-accent",
  primary: "bg-primary text-primary-foreground hover:bg-primary/90",
  destructive: "bg-status-error text-background hover:bg-status-error/90",
  ghost: "text-muted-foreground hover:bg-accent hover:text-foreground",
  link: "text-primary underline-offset-4 hover:underline",
  bare: "",
};

const sizeClass: Record<ActionSize, string> = {
  sm: "h-[calc(var(--control-h)-0.25rem)] px-3 text-meta",
  md: "h-(--control-h) px-4 text-body",
  icon: "h-[calc(var(--control-h)-0.25rem)] w-[calc(var(--control-h)-0.25rem)] p-0",
  xs: "h-6 px-2 text-meta",
  "icon-xs": "h-6 w-6 p-0",
  none: "",
};

export const ActionButton = forwardRef<HTMLButtonElement, ActionButtonProps>(
  (
    {
      intent = "default",
      size = "md",
      icon,
      loading = false,
      loadingLabel,
      disabledReason,
      disabled,
      title,
      tooltip,
      className,
      children,
      ...props
    },
    ref,
  ) => {
    const blocked = disabled || loading;
    const visibleIcon = loading ? (
      <Loader2 className="h-3.5 w-3.5 animate-spin" />
    ) : (
      icon
    );

    // ponytail: disabled reasons stay a native title (disabled buttons emit no
    // pointer events, so Radix cannot open); upgrade with a wrapper span later.
    const tip = tooltip ?? title;
    const label =
      props["aria-label"] ??
      ((size === "icon" || size === "icon-xs") && typeof tip === "string"
        ? tip
        : undefined);

    return (
      <Tooltip content={tip}>
        <button
          ref={ref}
          type="button"
          disabled={blocked}
          aria-label={label}
          title={disabledReason}
          className={cn(
            "inline-flex shrink-0 items-center justify-center gap-2 whitespace-nowrap rounded-md font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-50",
            intentClass[intent],
            sizeClass[size],
            className,
          )}
          {...props}
        >
          {visibleIcon}
          {size !== "icon" &&
            (loading && loadingLabel ? loadingLabel : children)}
        </button>
      </Tooltip>
    );
  },
);

ActionButton.displayName = "ActionButton";
