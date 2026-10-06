import { ModalShell } from "@/components/ui/modal-shell";
import { formatRelativeTime } from "@/lib/utils";
import type { SIEMForwarder } from "@/types";
import { LoadingSkeleton } from "@/components/form/loading-skeleton";
import { useSIEMForwarderStatus } from "./-hooks";

export function SIEMStatusDrawer({
  forwarder,
  onClose,
}: {
  forwarder: SIEMForwarder;
  onClose: () => void;
}) {
  const { data: status, isLoading } = useSIEMForwarderStatus(forwarder.id);

  const metric = (
    label: string,
    value: React.ReactNode,
    tone?: "error" | "warning",
  ) => (
    <div className="rounded-lg border border-border bg-card p-3">
      <p className="text-2xs uppercase tracking-wide text-muted-foreground">
        {label}
      </p>
      <p
        className={`mt-1 text-lg font-semibold tabular-nums ${
          tone === "error"
            ? "text-status-error"
            : tone === "warning"
              ? "text-status-warning"
              : "text-foreground"
        }`}
      >
        {value}
      </p>
    </div>
  );

  return (
    <ModalShell
      title="Forwarder Status"
      subtitle={forwarder.name}
      onClose={onClose}
      size="sm"
    >
      {isLoading && !status ? (
        <LoadingSkeleton label="Loading" heading />
      ) : (
        <>
          <div className="grid grid-cols-3 gap-3">
            {metric(
              "Queue depth",
              status?.queueDepth ?? 0,
              (status?.queueDepth ?? 0) > 0 ? "warning" : undefined,
            )}
            {metric("Dispatched", status?.dispatchedTotal ?? 0)}
            {metric(
              "Dropped",
              status?.droppedTotal ?? 0,
              (status?.droppedTotal ?? 0) > 0 ? "error" : undefined,
            )}
          </div>
          <div className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Last sent</span>
              <span className="text-foreground">
                {status?.lastSentAt
                  ? formatRelativeTime(status.lastSentAt)
                  : "Never"}
              </span>
            </div>
            <div className="flex items-start justify-between gap-4">
              <span className="text-muted-foreground shrink-0">Last error</span>
              <span
                className={`text-right ${status?.lastError ? "text-status-error" : "text-muted-foreground"}`}
              >
                {status?.lastError || "None"}
              </span>
            </div>
          </div>
        </>
      )}
    </ModalShell>
  );
}
