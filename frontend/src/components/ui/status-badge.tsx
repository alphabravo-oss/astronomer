
import type { ReactNode } from "react";
import { cn, statusBgColor, statusDotColor } from "@/lib/utils";
import { cva, type VariantProps } from "class-variance-authority";

const statusBadgeVariants = cva(
  "inline-flex items-center gap-1.5 font-medium transition-colors",
  {
    variants: {
      size: {
        sm: "px-2 py-0.5 text-2xs leading-4",
        md: "px-2.5 py-0.5 text-xs leading-5",
        lg: "px-3 py-1 text-sm leading-5",
      },
      shape: {
        pill: "rounded-full",
        square: "rounded-md",
      },
    },
    defaultVariants: {
      size: "md",
      shape: "pill",
    },
  },
);

/**
 * Arbitrary, user-chosen tag colors (e.g. a cluster's "Production" badge).
 * Unlike `status`, these are not derived from a state word — an unknown
 * value falls back to `slate`, matching the pre-unification ClusterBadge.
 */
export const CLUSTER_BADGE_TONES = [
  "slate",
  "blue",
  "green",
  "amber",
  "red",
  "purple",
] as const;
export type ClusterBadgeTone = (typeof CLUSTER_BADGE_TONES)[number];

const toneClasses: Record<ClusterBadgeTone, string> = {
  slate: "border-border bg-muted text-muted-foreground",
  blue: "border-status-info/30 bg-status-info/10 text-status-info",
  green: "border-status-success/30 bg-status-success/10 text-status-success",
  amber: "border-status-warning/30 bg-status-warning/10 text-status-warning",
  red: "border-status-error/30 bg-status-error/10 text-status-error",
  purple: "border-primary/30 bg-primary/10 text-primary",
};

interface StatusBadgeProps extends VariantProps<typeof statusBadgeVariants> {
  /**
   * Wire-backed status fields can be absent while a controller is still
   * discovering an object. Treat that as an explicit unknown state instead
   * of letting one partial payload crash the surrounding operator page.
   */
  status?: string | null;
  label?: string;
  icon?: ReactNode;
  showDot?: boolean;
  pulse?: boolean;
  className?: string;
  /** Renders only the colored dot (`aria-label`/`title` carry the text). */
  dotOnly?: boolean;
  /**
   * Opts into the arbitrary-color "tag" rendering (uppercase, no dot) used
   * by user-assigned labels like a cluster badge, instead of deriving color
   * from a normalized status word. Accepts any string; an unrecognized tone
   * falls back to `slate`.
   */
  tone?: string;
}

export function StatusBadge({
  status,
  label,
  icon,
  showDot: showDotProp,
  pulse = false,
  size,
  shape = "pill",
  className,
  dotOnly = false,
  tone,
}: StatusBadgeProps) {
  if (tone !== undefined) {
    const text = (label ?? status ?? "").trim();
    if (!text) return null;
    return (
      <span
        className={cn(
          "inline-flex max-w-48 items-center truncate rounded-full border px-2 py-0.5 text-xs font-semibold uppercase tracking-wide",
          toneClasses[tone as ClusterBadgeTone] ?? toneClasses.slate,
          className,
        )}
        title={text}
      >
        {text}
      </span>
    );
  }

  const normalizedStatus = status?.trim() || "unknown";
  const displayLabel =
    label ||
    normalizedStatus.charAt(0).toUpperCase() +
      normalizedStatus.slice(1).replace(/([A-Z])/g, " $1");
  const isActive = [
    "active",
    "healthy",
    "running",
    "ready",
    "completed",
    "succeeded",
    "synced",
    "connected",
  ].includes(normalizedStatus.toLowerCase());
  const showDot = showDotProp ?? shape !== "square";

  if (dotOnly) {
    return (
      <span
        aria-label={displayLabel}
        title={displayLabel}
        className={cn(
          "relative inline-flex h-1.5 w-1.5 shrink-0",
          className,
        )}
      >
        {(pulse || isActive) && (
          <span
            className={cn(
              "absolute inline-flex h-full w-full rounded-full opacity-75 animate-pulse-dot",
              statusDotColor(normalizedStatus),
            )}
          />
        )}
        <span
          className={cn(
            "relative inline-flex rounded-full h-1.5 w-1.5",
            statusDotColor(normalizedStatus),
          )}
        />
      </span>
    );
  }

  return (
    <span
      className={cn(
        statusBadgeVariants({ size, shape }),
        statusBgColor(normalizedStatus),
        className,
      )}
    >
      {icon ? (
        <span className="inline-flex h-3 w-3 shrink-0 items-center justify-center">
          {icon}
        </span>
      ) : null}
      {!icon && showDot && (
        <span className="relative flex h-1.5 w-1.5">
          {(pulse || isActive) && (
            <span
              className={cn(
                "absolute inline-flex h-full w-full rounded-full opacity-75 animate-pulse-dot",
                statusDotColor(normalizedStatus),
              )}
            />
          )}
          <span
            className={cn(
              "relative inline-flex rounded-full h-1.5 w-1.5",
              statusDotColor(normalizedStatus),
            )}
          />
        </span>
      )}
      {displayLabel}
    </span>
  );
}

/** Renders just the colored status dot (e.g. a terminal-tab connection light). */
export function StatusDot({
  status,
  label,
  pulse,
  className,
}: Pick<StatusBadgeProps, "status" | "label" | "pulse" | "className">) {
  return (
    <StatusBadge
      status={status}
      label={label}
      pulse={pulse}
      dotOnly
      className={className}
    />
  );
}
