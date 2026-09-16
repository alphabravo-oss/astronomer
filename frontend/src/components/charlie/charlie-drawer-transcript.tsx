import type { RefObject } from "react";
import { Bot, Command } from "lucide-react";
import { cn } from "@/lib/utils";
import { EmptyState } from "@/components/ui/empty-state";
import { SafeMarkdown } from "./safe-markdown";
import { CharlieMessageParts } from "./message-parts";
import {
  CharlieProgressIndicator,
  CopyMessageButton,
} from "./charlie-drawer-primitives";
import {
  initialCharlieTurnProgress,
  type CharlieTurnProgress,
} from "./turn-progress";
import { parseCharlieCommand } from "./commands";
import type {
  CharlieMessage,
  CharlieCommandDescriptor,
} from "@/lib/api/charlie";

export function CharlieTranscript({
  messages,
  catalogCommands,
  viewingThreadId,
  onReturn,
  showProgress,
  turnProgress,
  messagesViewportRef,
  stickToBottomRef,
  onApprovalChanged,
}: {
  messages: CharlieMessage[];
  catalogCommands: CharlieCommandDescriptor[];
  viewingThreadId?: string;
  onReturn: () => void;
  showProgress: boolean;
  turnProgress?: CharlieTurnProgress;
  messagesViewportRef: RefObject<HTMLDivElement | null>;
  stickToBottomRef: RefObject<boolean>;
  onApprovalChanged: () => void;
}) {
  return (
    <div
      ref={messagesViewportRef}
      className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain px-5 py-3 select-text"
      role="log"
      aria-live="polite"
      aria-relevant="additions text"
      aria-busy={showProgress}
      aria-label="Charlie conversation"
      onScroll={(e) => {
        const el = e.currentTarget;
        const distanceFromBottom =
          el.scrollHeight - el.scrollTop - el.clientHeight;
        stickToBottomRef.current = distanceFromBottom < 80;
      }}
    >
      {viewingThreadId ? (
        <div
          className="flex items-center justify-between gap-3 rounded-md border bg-muted/50 p-3"
          role="status"
        >
          <div>
            <p className="text-sm font-medium">
              Viewing a previous conversation
            </p>
            <p className="text-xs text-muted-foreground">
              This transcript is read-only and is not added to your current
              Charlie context.
            </p>
          </div>
          <button
            type="button"
            onClick={onReturn}
            className="shrink-0 rounded-md border px-2 py-1 text-xs"
          >
            Back to current
          </button>
        </div>
      ) : null}
      {messages.length === 0 && !showProgress ? (
        <EmptyState
          icon={Bot}
          title="Ask Charlie"
          description="Investigate, explain, or plan work using the selected context."
        />
      ) : (
        <>
          {messages.map((m) => {
            const recognizedCommand =
              m.role === "user"
                ? parseCharlieCommand(m.content, catalogCommands)
                : undefined;
            return (
              <article
                key={m.id}
                aria-label={
                  m.role === "user"
                    ? "Message from you"
                    : "Message from Charlie"
                }
                className={cn(
                  "rounded-lg border p-3 select-text",
                  m.role === "user" ? "ml-8 bg-primary/5" : "mr-8 bg-card",
                  recognizedCommand && "border-primary/40 bg-primary/10",
                )}
              >
                <div className="mb-1 flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <p className="text-xs font-medium text-muted-foreground">
                      {m.role === "user" ? "You" : "Charlie"}
                    </p>
                    {recognizedCommand ? (
                      <span
                        aria-label="Recognized Charlie command"
                        title={recognizedCommand.descriptor.label}
                        className="inline-flex items-center gap-1 rounded-full bg-primary/15 px-1.5 py-0.5 text-[10px] font-semibold uppercase tracking-wide text-primary"
                      >
                        <Command className="h-2.5 w-2.5" aria-hidden="true" />
                        Command
                      </span>
                    ) : null}
                  </div>
                  {m.role === "assistant" && m.content?.trim() ? (
                    <CopyMessageButton text={m.content} />
                  ) : null}
                </div>
                {recognizedCommand ? (
                  <p className="break-words font-mono text-sm font-medium text-primary">
                    {m.content}
                  </p>
                ) : (
                  <SafeMarkdown streaming={m.state === "streaming"}>
                    {m.content}
                  </SafeMarkdown>
                )}
                <CharlieMessageParts
                  message={m}
                  onApprovalChanged={onApprovalChanged}
                />
              </article>
            );
          })}
          {showProgress && (
            <CharlieProgressIndicator
              progress={turnProgress ?? initialCharlieTurnProgress()}
            />
          )}
        </>
      )}
    </div>
  );
}
