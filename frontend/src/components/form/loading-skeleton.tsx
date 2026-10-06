import { cn } from "@/lib/utils";
import { Skeleton, SkeletonCard, SkeletonText } from "@/components/ui/skeleton";

interface LoadingSkeletonProps {
  /** Accessible name announced for the busy region, e.g. "Loading webhooks". */
  label: string;
  /** Text placeholder rows (ignored when `cards` is set). */
  lines?: number;
  /** Render a heading bar above the text rows. */
  heading?: boolean;
  /** Render a responsive grid of this many card placeholders instead of text. */
  cards?: number;
  className?: string;
}

/** Busy region for page, list and detail loads; the skeleton children are decorative. */
export function LoadingSkeleton({
  label,
  lines = 4,
  heading = false,
  cards,
  className,
}: LoadingSkeletonProps) {
  return (
    <div
      role="status"
      aria-busy="true"
      aria-label={label}
      className={cn(
        cards
          ? "grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3"
          : "space-y-3",
        className,
      )}
    >
      {cards ? (
        Array.from({ length: cards }, (_, i) => <SkeletonCard key={i} />)
      ) : (
        <>
          {heading && <Skeleton className="h-5 w-1/3" />}
          <SkeletonText lines={lines} />
        </>
      )}
    </div>
  );
}
