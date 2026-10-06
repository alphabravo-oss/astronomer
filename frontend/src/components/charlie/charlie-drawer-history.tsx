import { SkeletonText } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import type { CharlieConversation } from "./use-charlie-conversation";
import type { CharlieCommandDescriptor } from "@/lib/api/charlie";
import { ActionButton } from "@/components/ui/action-button";

export function CharlieHistory({
  threads,
  threadId,
  viewingThreadId,
  onClose,
  onSelect,
}: {
  threads: CharlieConversation["threads"];
  threadId?: string;
  viewingThreadId?: string;
  onClose: () => void;
  onSelect: (id: string | undefined) => void;
}) {
  return (
    <section
      className="max-h-52 shrink-0 overflow-y-auto border-b border-border bg-card px-5 py-3"
      aria-label="Recent Charlie conversations"
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <p className="text-xs font-semibold">Recent conversations</p>
        <ActionButton
          intent="bare"
          size="none"
          aria-label="Close conversation history"
          onClick={onClose}
          className="text-xs text-muted-foreground"
        >
          Close
        </ActionButton>
      </div>
      {threads.isLoading ? (
        <div role="status" aria-busy="true">
          <span className="sr-only">Loading conversations…</span>
          <SkeletonText lines={2} />
        </div>
      ) : null}
      {threads.isError ? (
        <p role="alert" className="text-xs text-muted-foreground">
          Conversation history is unavailable.
        </p>
      ) : null}
      <div className="space-y-1">
        {threads.data?.map((thread) => {
          const current = thread.id === threadId;
          const selected = current
            ? !viewingThreadId
            : thread.id === viewingThreadId;
          return (
            <ActionButton
              intent="bare"
              size="none"
              key={thread.id}
              aria-current={selected ? "true" : undefined}
              onClick={() => onSelect(current ? undefined : thread.id)}
              className={cn(
                "block w-full whitespace-normal rounded-md border px-3 py-2 text-left font-normal hover:bg-accent",
                selected && "border-primary bg-primary/5",
              )}
            >
              <span className="block truncate text-sm font-medium">
                {thread.title || "Untitled conversation"}
              </span>
              <span className="block text-[11px] text-muted-foreground">
                {current
                  ? "Current"
                  : thread.state === "archived"
                    ? "Previous"
                    : thread.state}
                {thread.updated_at
                  ? ` · ${new Date(thread.updated_at).toLocaleString()}`
                  : ""}
              </span>
            </ActionButton>
          );
        })}
      </div>
      {!threads.isLoading && threads.data?.length === 0 ? (
        <p className="text-xs text-muted-foreground">
          No previous conversations yet.
        </p>
      ) : null}
    </section>
  );
}

export function CharlieCommandHelp({
  catalogCommands,
  onClose,
  onSelect,
}: {
  catalogCommands: CharlieCommandDescriptor[];
  onClose: () => void;
  onSelect: (command: CharlieCommandDescriptor) => void;
}) {
  return (
    <section
      className="max-h-64 shrink-0 overflow-y-auto border-b border-border bg-card px-5 py-3"
      aria-label="Charlie command help"
    >
      <div className="mb-2 flex items-center justify-between gap-2">
        <div>
          <p className="text-sm font-semibold">Charlie commands</p>
          <p className="text-xs text-muted-foreground">
            Shortcuts use the same scope, mode, approvals, and audit controls as
            ordinary chat.
          </p>
        </div>
        <ActionButton
          intent="bare"
          size="none"
          aria-label="Close command help"
          onClick={onClose}
          className="text-xs text-muted-foreground"
        >
          Close
        </ActionButton>
      </div>
      <div className="space-y-1">
        {catalogCommands.map((command) => (
          <ActionButton
            intent="bare"
            size="none"
            key={command.id}
            onClick={() => onSelect(command)}
            className="block w-full whitespace-normal rounded-md px-2 py-1.5 text-left font-normal hover:bg-accent"
          >
            <span className="font-mono text-xs">
              /{command.name}
              {command.argument ? ` <${command.argument.placeholder}>` : ""}
            </span>
            <span className="ml-2 text-xs text-muted-foreground">
              {command.description}
            </span>
          </ActionButton>
        ))}
      </div>
    </section>
  );
}
