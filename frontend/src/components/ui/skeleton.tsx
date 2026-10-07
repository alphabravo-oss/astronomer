import type { HTMLAttributes } from "react";
import { TableCell, TableRow } from "@/components/ui/table";
import { cn } from "@/lib/utils";

// Decorative placeholder; the surrounding region owns aria-busy/aria-live.
export function Skeleton({
  className,
  ...props
}: HTMLAttributes<HTMLDivElement>) {
  return (
    <div
      aria-hidden="true"
      className={cn("animate-pulse-dot rounded-md bg-muted", className)}
      {...props}
    />
  );
}

export function SkeletonText({
  lines = 3,
  className,
}: {
  lines?: number;
  className?: string;
}) {
  return (
    <div className={cn("space-y-2", className)}>
      {Array.from({ length: lines }, (_, i) => (
        <Skeleton
          key={i}
          className={cn("h-3", i === lines - 1 ? "w-2/3" : "w-full")}
        />
      ))}
    </div>
  );
}

export function SkeletonCard({ className }: { className?: string }) {
  return (
    <div
      className={cn("rounded-lg border border-border bg-card p-4", className)}
    >
      <Skeleton className="h-3 w-24" />
      <Skeleton className="mt-3 h-7 w-16" />
      <Skeleton className="mt-3 h-3 w-32" />
    </div>
  );
}

export function SkeletonTableRows({
  rows = 5,
  columns = 5,
}: {
  rows?: number;
  columns?: number;
}) {
  return (
    <>
      {Array.from({ length: rows }, (_, r) => (
        <TableRow key={r} className="border-b border-border last:border-0">
          {Array.from({ length: columns }, (_, c) => (
            <TableCell key={c} className="px-4 py-3">
              <Skeleton className={cn("h-3", c === 0 ? "w-32" : "w-16")} />
            </TableCell>
          ))}
        </TableRow>
      ))}
    </>
  );
}
