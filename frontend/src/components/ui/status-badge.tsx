"use client";

import type { ReactNode } from "react";
import { cn, statusBgColor, statusDotColor } from "@/lib/utils";
import { cva, type VariantProps } from "class-variance-authority";

const statusBadgeVariants = cva(
  "inline-flex items-center gap-1.5 rounded-full font-medium transition-colors",
  {
    variants: {
      size: {
        sm: "px-2 py-0.5 text-[10px] leading-4",
        md: "px-2.5 py-0.5 text-xs leading-5",
        lg: "px-3 py-1 text-sm leading-5",
      },
    },
    defaultVariants: {
      size: "md",
    },
  },
);

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
}

export function StatusBadge({
  status,
  label,
  icon,
  showDot = true,
  pulse = false,
  size,
  className,
}: StatusBadgeProps) {
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

  return (
    <span
      className={cn(
        statusBadgeVariants({ size }),
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
