import { History as HistoryIcon, Loader2, StopCircle } from "lucide-react";
import { cn } from "@/lib/utils";
import type { CharlieModePresentation } from "./charlie-mode";

export function CharlieModeSummary({
  mode,
  modeSettling,
}: {
  mode: CharlieModePresentation;
  modeSettling: boolean;
}) {
  return (
    <span className="flex flex-wrap items-center gap-2">
      <span>AI assistance within your authorized Astronomer scope</span>
      <span
        className={cn(
          "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-semibold",
          modeSettling
            ? "border-status-info/40 bg-status-info/10 text-status-info"
            : mode.badgeClass,
        )}
        aria-label={
          modeSettling
            ? `Charlie mode changing: ${mode.label}`
            : `Current Charlie mode: ${mode.label}`
        }
        data-testid="charlie-mode-badge"
        data-mode={mode.key}
        data-settling={modeSettling ? "true" : "false"}
      >
        {modeSettling ? (
          <Loader2 className="h-3 w-3 animate-spin motion-reduce:animate-none" />
        ) : null}
        Mode: {mode.label}
        {modeSettling ? " · settling agents" : ""}
      </span>
      <span className="basis-full text-xs text-muted-foreground">
        {modeSettling
          ? "Mode ceiling is rolling out. Wait until both product-agent replicas are verified before relying on writes."
          : mode.ceiling}
      </span>
    </span>
  );
}

export function CharlieHeaderActions({
  conversationListOpen,
  onToggleHistory,
  onNewChat,
  newChatPending,
  canAbort,
  onAbort,
}: {
  conversationListOpen: boolean;
  onToggleHistory: () => void;
  onNewChat: () => void;
  newChatPending: boolean;
  canAbort: boolean;
  onAbort: () => void;
}) {
  return (
    <div className="flex items-center gap-1">
      <button
        type="button"
        onClick={onToggleHistory}
        aria-expanded={conversationListOpen}
        className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs"
      >
        <HistoryIcon className="h-3 w-3" /> History
      </button>
      <button
        type="button"
        onClick={onNewChat}
        disabled={newChatPending}
        className="rounded-md border px-2 py-1 text-xs"
      >
        New chat
      </button>
      {canAbort && (
        <button
          type="button"
          onClick={onAbort}
          className="inline-flex items-center gap-1 rounded-md border border-status-error/40 px-2 py-1 text-xs text-status-error"
        >
          <StopCircle className="h-3 w-3" /> Abort turn
        </button>
      )}
    </div>
  );
}
