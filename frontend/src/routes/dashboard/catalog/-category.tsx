import { cn } from "@/lib/utils";
import type { HelmChartCategory } from "@/types";

export const categories: { key: HelmChartCategory | "all"; label: string }[] = [
  { key: "all", label: "All" },
  { key: "monitoring", label: "Monitoring" },
  { key: "logging", label: "Logging" },
  { key: "security", label: "Security" },
  { key: "database", label: "Database" },
  { key: "networking", label: "Networking" },
  { key: "storage", label: "Storage" },
  { key: "messaging", label: "Messaging" },
  { key: "ci-cd", label: "CI/CD" },
  { key: "other", label: "Other" },
];

const categoryColors: Record<string, string> = {
  monitoring: "bg-status-info/10 text-status-info",
  logging: "bg-status-success/10 text-status-success",
  security: "bg-status-error/10 text-status-error",
  database: "bg-status-pending/10 text-status-pending",
  networking: "bg-status-high/10 text-status-high",
  storage: "bg-status-neutral/10 text-status-neutral",
  messaging: "bg-status-warning/10 text-status-warning",
  "ci-cd": "bg-primary/10 text-primary",
  other: "bg-muted text-muted-foreground",
};

export function CategoryChip({
  category,
  className,
}: {
  category: string;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "rounded-sm font-medium",
        categoryColors[category] || categoryColors.other,
        className,
      )}
    >
      {category}
    </span>
  );
}
