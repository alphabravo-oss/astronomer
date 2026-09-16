import { Command, ExternalLink, Loader2, Send } from "lucide-react";
import { Link as RouterLink } from "@tanstack/react-router";
import { cn } from "@/lib/utils";
import { commandInsertion } from "./commands";
import type { CharlieComposerModel } from "./use-charlie-composer";
import type { CharlieConversation } from "./use-charlie-conversation";

function charlieErrorStatus(error: unknown): number | undefined {
  if (!error || typeof error !== "object") return undefined;
  const e = error as {
    status?: number;
    response?: { status?: number };
  };
  return e.status ?? e.response?.status;
}

function isCharlieRateLimitedError(error: unknown): boolean {
  return charlieErrorStatus(error) === 429;
}

export function CharlieComposerFeedback({
  conversation,
  commandNotice,
}: {
  conversation: Pick<
    CharlieConversation,
    "send" | "history" | "abort" | "streamUnavailable" | "turnFailed"
  >;
  commandNotice?: string;
}) {
  const { send, history, abort, streamUnavailable, turnFailed } = conversation;
  return (
    <>
      {(send.isError || history.isError || streamUnavailable || turnFailed) && (
        <div
          role="alert"
          className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm"
        >
          <p>
            {turnFailed
              ? "Charlie could not complete this request. Partial work was not presented as an answer, and your Astronomer data remains unchanged. You can retry or narrow the request."
              : streamUnavailable
                ? "The live Charlie stream is reconnecting. Confirmed history remains available, and your Astronomer data remains unchanged."
                : isCharlieRateLimitedError(send.error) ||
                    isCharlieRateLimitedError(history.error)
                  ? "Charlie is briefly rate-limited while the session catches up. Retry in a moment — your Astronomer data remains unchanged."
                  : "Charlie is unavailable or access was denied. Your Astronomer data remains unchanged."}
          </p>
          <div className="mt-2 flex gap-2">
            {history.isError && (
              <button
                type="button"
                onClick={() => void history.refetch()}
                className="rounded-sm border px-2 py-1 text-xs"
              >
                Reconnect and retry history
              </button>
            )}
            {(send.isError || turnFailed) && send.variables && (
              <button
                type="button"
                onClick={() => send.mutate(send.variables!)}
                className="rounded-sm border px-2 py-1 text-xs"
              >
                Retry message
              </button>
            )}
          </div>
        </div>
      )}
      {abort.isSuccess && (
        <p role="status" className="rounded-md border p-2 text-xs">
          This Charlie session was aborted. Its authority has been revoked.
        </p>
      )}
      {abort.isError && (
        <p
          role="alert"
          className="rounded-md border border-status-error/40 p-2 text-xs text-status-error"
        >
          Abort is pending or could not be confirmed. The product-side session
          remains locally closed.
        </p>
      )}
      {commandNotice ? (
        <p role="status" className="rounded-md border bg-muted/50 p-2 text-xs">
          {commandNotice}
        </p>
      ) : null}
    </>
  );
}

export function CharlieComposer({
  model,
  sessionId,
  viewingThreadId,
  historyReady,
  awaitingReply,
  send,
  onReturn,
}: {
  model: CharlieComposerModel;
  sessionId?: string;
  viewingThreadId?: string;
  historyReady: boolean;
  awaitingReply: boolean;
  send: CharlieConversation["send"];
  onReturn: () => void;
}) {
  const {
    text,
    setText,
    setCommandNotice,
    suggestedCommands,
    slashSuggestions,
    selectedCommandIndex,
    submitComposer,
    onKeyDown,
  } = model;
  return (
    <>
      {viewingThreadId ? (
        <div className="flex items-center justify-between gap-3 rounded-md border p-3">
          <p className="text-xs text-muted-foreground">
            Previous conversations are read-only. Return to the current
            conversation to message Charlie.
          </p>
          <button
            type="button"
            onClick={onReturn}
            className="shrink-0 rounded-md bg-primary px-3 py-2 text-xs text-primary-foreground"
          >
            Return to current
          </button>
        </div>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            submitComposer();
          }}
          className="space-y-2"
        >
          {!text && suggestedCommands.length > 0 ? (
            <div
              className="flex flex-wrap gap-1.5"
              aria-label="Suggested Charlie commands"
            >
              {suggestedCommands.map((command) => (
                <button
                  type="button"
                  key={command.id}
                  onClick={() => setText(commandInsertion(command))}
                  className="rounded-full border px-2 py-1 font-mono text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
                >
                  /{command.name}
                </button>
              ))}
            </div>
          ) : null}
          {text.trimStart().startsWith("/") && slashSuggestions.length > 0 ? (
            <div
              id="charlie-command-suggestions"
              role="listbox"
              aria-label="Charlie command suggestions"
              className="max-h-56 overflow-y-auto rounded-md border bg-popover p-1 shadow-md"
            >
              {slashSuggestions.map((command, index) => (
                <button
                  type="button"
                  role="option"
                  aria-selected={index === selectedCommandIndex}
                  key={command.id}
                  onMouseDown={(event) => event.preventDefault()}
                  onClick={() => setText(commandInsertion(command))}
                  className={cn(
                    "flex w-full items-start gap-2 rounded-sm px-2 py-2 text-left",
                    index === selectedCommandIndex
                      ? "bg-accent"
                      : "hover:bg-accent",
                  )}
                >
                  <Command className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" />
                  <span className="min-w-0">
                    <span className="block font-mono text-xs">
                      /{command.name}
                      {command.argument
                        ? ` <${command.argument.placeholder}>`
                        : ""}
                    </span>
                    <span className="block text-[11px] text-muted-foreground">
                      {command.description}
                    </span>
                  </span>
                </button>
              ))}
            </div>
          ) : null}
          <textarea
            aria-label="Message Charlie"
            aria-controls={
              slashSuggestions.length
                ? "charlie-command-suggestions"
                : undefined
            }
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              setCommandNotice(undefined);
            }}
            onKeyDown={onKeyDown}
            rows={3}
            maxLength={sessionId ? 32768 : 4096}
            className="w-full resize-none rounded-lg border bg-background p-3 text-sm"
            placeholder="Ask Charlie or type / for commands…"
          />
          <div className="flex justify-between">
            <RouterLink
              to="/dashboard/charlie"
              search={{
                tab: "conversations",
                session: sessionId ?? undefined,
              }}
              className="inline-flex items-center gap-1 text-xs text-primary"
            >
              Open Charlie hub
              <ExternalLink className="h-3 w-3" />
            </RouterLink>
            <button
              type="submit"
              disabled={
                !text.trim() || !historyReady || send.isPending || awaitingReply
              }
              className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground transition-colors motion-reduce:transition-none disabled:opacity-50"
            >
              {send.isPending || awaitingReply ? (
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Send className="h-4 w-4" />
              )}
              {send.isPending ? "Sending" : awaitingReply ? "Working" : "Send"}
            </button>
          </div>
        </form>
      )}
    </>
  );
}
