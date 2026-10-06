import type { ElementType, ReactNode } from "react";
import { Info, TriangleAlert } from "lucide-react";
import { cn } from "@/lib/utils";

type CalloutTone = "info" | "warning";

const toneClass: Record<CalloutTone, string> = {
  info: "border-status-info/30 bg-status-info/10",
  warning: "border-status-warning/30 bg-status-warning/10",
};
const iconClass: Record<CalloutTone, string> = {
  info: "text-status-info",
  warning: "text-status-warning",
};
const defaultIcon: Record<CalloutTone, ElementType> = {
  info: Info,
  warning: TriangleAlert,
};

/**
 * Inline scope/context notice that sits directly under a `PageHeader`.
 * Use it for "this page is cluster-wide"-style notes that are too long for the
 * header description (which stays one sentence). Not a toast and not a modal.
 */
export function InfoCallout({
  children,
  tone = "info",
  icon,
  action,
  className,
}: {
  children: ReactNode;
  tone?: CalloutTone;
  icon?: ElementType;
  action?: ReactNode;
  className?: string;
}) {
  const Icon = icon ?? defaultIcon[tone];
  return (
    <div
      role={tone === "warning" ? "alert" : "note"}
      className={cn(
        "flex items-start gap-3 rounded-lg border px-4 py-3 text-sm text-foreground",
        toneClass[tone],
        className,
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn("mt-0.5 h-4 w-4 shrink-0", iconClass[tone])}
      />
      <div className="min-w-0 flex-1">{children}</div>
      {action ? <div className="shrink-0">{action}</div> : null}
    </div>
  );
}
