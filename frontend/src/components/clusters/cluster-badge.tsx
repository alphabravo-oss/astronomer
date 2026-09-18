import { cn } from "@/lib/utils";

const badgeColors: Record<string, string> = {
  slate: "border-border bg-muted text-muted-foreground",
  blue: "border-status-info/30 bg-status-info/10 text-status-info",
  green: "border-status-success/30 bg-status-success/10 text-status-success",
  amber: "border-status-warning/30 bg-status-warning/10 text-status-warning",
  red: "border-status-error/30 bg-status-error/10 text-status-error",
  purple: "border-primary/30 bg-primary/10 text-primary",
};

export const CLUSTER_BADGE_COLORS = [
  "slate",
  "blue",
  "green",
  "amber",
  "red",
  "purple",
] as const;
export type ClusterBadgeColor = (typeof CLUSTER_BADGE_COLORS)[number];

export function ClusterBadge({
  text,
  color,
  className,
}: {
  text?: string;
  color?: string;
  className?: string;
}) {
  const label = text?.trim();
  if (!label) return null;

  return (
    <span
      className={cn(
        "inline-flex max-w-48 items-center truncate rounded-full border px-2 py-0.5 text-xs font-semibold uppercase tracking-wide",
        badgeColors[color ?? ""] ?? badgeColors.slate,
        className,
      )}
      title={label}
    >
      {label}
    </span>
  );
}
