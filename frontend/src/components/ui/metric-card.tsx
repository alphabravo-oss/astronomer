
import { Link as RouterLink } from "@tanstack/react-router";
import type { ReactNode } from "react";
import { cn, gaugeColor, gaugeTextColor } from "@/lib/utils";
import { ArrowUpRight, ArrowDownRight, Minus } from "lucide-react";

type MetricCardTone = "default" | "success" | "warning" | "error";

const toneTextClasses: Record<MetricCardTone, string> = {
  default: "text-foreground",
  success: "text-status-success",
  warning: "text-status-warning",
  error: "text-status-error",
};

interface MetricCardProps {
  /** Preferred name for the metric's heading. `title` is kept as an alias. */
  label?: string;
  title?: string;
  value: ReactNode;
  unit?: string;
  subtitle?: string;
  trend?: "up" | "down" | "flat";
  trendValue?: string;
  percentage?: number;
  thresholdWarning?: number;
  thresholdCritical?: number;
  icon?: React.ReactNode;
  sparkline?: number[];
  className?: string;
  /** Navigates the whole card when set, instead of a static tile. */
  href?: string;
  /** Overrides the percentage-derived value color with a fixed tone. */
  tone?: MetricCardTone;
  /** Tighter padding/type-scale for compact grids. */
  dense?: boolean;
}

export function MetricCard({
  label,
  title,
  value,
  unit,
  subtitle,
  trend,
  trendValue,
  percentage,
  icon,
  sparkline,
  className,
  href,
  tone,
  dense = false,
}: MetricCardProps) {
  const heading = label ?? title;
  const Wrapper = href ? RouterLink : "div";
  const wrapperProps = href ? { to: href } : {};
  return (
    <Wrapper
      {...wrapperProps}
      className={cn(
        "block rounded-lg border border-border bg-card transition-colors hover:bg-card/80",
        dense ? "p-3" : "p-5",
        className,
      )}
    >
      <div className="flex items-start justify-between">
        <div className="space-y-1">
          {heading && (
            <p className="text-sm font-medium text-muted-foreground">
              {heading}
            </p>
          )}
          <div className="flex items-baseline gap-1.5">
            <span
              className={cn(
                dense ? "text-lg font-semibold tracking-tight" : "text-2xl font-semibold tracking-tight",
                tone
                  ? toneTextClasses[tone]
                  : percentage !== undefined
                    ? gaugeTextColor(percentage)
                    : "text-foreground",
              )}
            >
              {value}
            </span>
            {unit && (
              <span className="text-sm text-muted-foreground">{unit}</span>
            )}
          </div>
          {subtitle && (
            <p className="text-xs text-muted-foreground">{subtitle}</p>
          )}
        </div>

        <div className="flex flex-col items-end gap-2">
          {icon && (
            <div className="rounded-md bg-muted p-2 text-muted-foreground">
              {icon}
            </div>
          )}

          {trend && trendValue && (
            <div
              className={cn(
                "flex items-center gap-0.5 text-xs font-medium",
                trend === "up" && "text-status-error",
                trend === "down" && "text-status-success",
                trend === "flat" && "text-muted-foreground",
              )}
            >
              {trend === "up" && <ArrowUpRight className="h-3 w-3" />}
              {trend === "down" && <ArrowDownRight className="h-3 w-3" />}
              {trend === "flat" && <Minus className="h-3 w-3" />}
              {trendValue}
            </div>
          )}
        </div>
      </div>

      {/* Gauge bar for percentage values */}
      {percentage !== undefined && (
        <div className="mt-3">
          <div className="gauge-bar">
            <div
              className={cn("gauge-bar-fill", gaugeColor(percentage))}
              style={{ width: `${Math.min(percentage, 100)}%` }}
            />
          </div>
        </div>
      )}

      {/* Mini sparkline */}
      {sparkline && sparkline.length > 0 && (
        <div className="mt-3 flex items-end gap-px h-8">
          {sparkline.map((v, i) => {
            const max = Math.max(...sparkline);
            const height = max > 0 ? (v / max) * 100 : 0;
            return (
              <div
                key={i}
                className="flex-1 rounded-t-sm bg-primary/20 transition-all"
                style={{ height: `${Math.max(height, 4)}%` }}
              />
            );
          })}
        </div>
      )}
    </Wrapper>
  );
}
