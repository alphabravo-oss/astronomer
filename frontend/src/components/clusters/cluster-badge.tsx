import { cn } from "@/lib/utils";

const badgeColors: Record<string, string> = {
  slate:
    "border-slate-500/30 bg-slate-500/10 text-slate-700 dark:text-slate-300",
  blue: "border-blue-500/30 bg-blue-500/10 text-blue-700 dark:text-blue-300",
  green:
    "border-green-500/30 bg-green-500/10 text-green-700 dark:text-green-300",
  amber:
    "border-amber-500/30 bg-amber-500/10 text-amber-800 dark:text-amber-300",
  red: "border-red-500/30 bg-red-500/10 text-red-700 dark:text-red-300",
  purple:
    "border-purple-500/30 bg-purple-500/10 text-purple-700 dark:text-purple-300",
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
