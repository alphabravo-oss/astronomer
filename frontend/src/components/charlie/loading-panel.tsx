import { SkeletonText } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";

/**
 * Skeleton placeholder for a Charlie page/tab/list while its query loads. The
 * visible label is screen-reader only so the region announces what is loading.
 */
export function LoadingPanel({
  title,
  lines = 3,
  className,
}: {
  title: string;
  lines?: number;
  className?: string;
}) {
  return (
    <div
      role="status"
      aria-busy="true"
      className={cn(
        "rounded-xl border border-border bg-card p-(--card-p)",
        className,
      )}
    >
      <span className="sr-only">{title}</span>
      <SkeletonText lines={lines} />
    </div>
  );
}
