import { LoadingPanel } from "@/components/charlie/loading-panel";
import { useQuery } from "@tanstack/react-query";
import { Bot } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { SafeMarkdown } from "@/components/charlie/safe-markdown";
import { CharlieMessageParts } from "@/components/charlie/message-parts";
import { getCharlieThreadHistory, listCharlieThreads } from "@/lib/api/charlie";
import { cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { ActionButton } from "@/components/ui/action-button";
import { QueryFailure } from "./-shared";

export function Conversations({
  selected,
  onSelect,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const q = useQuery({
    queryKey: queryKeys.charlie.threads,
    queryFn: listCharlieThreads,
    retry: false,
  });
  // Interactive threads only — server list is owner-scoped user chats.
  const rows = q.data ?? [];
  const selectedConversation = rows.some((thread) => thread.id === selected)
    ? selected
    : null;
  const h = useQuery({
    queryKey: queryKeys.charlie.threadHistory(selectedConversation),
    queryFn: () => getCharlieThreadHistory(selectedConversation!),
    enabled: !!selectedConversation,
    retry: false,
  });
  if (q.isLoading)
    return <LoadingPanel title="Loading conversations" lines={2} />;
  if (q.isError) return <QueryFailure label="Conversations" query={q} />;
  return (
    <div className="grid gap-4 md:grid-cols-[18rem_1fr]">
      <div className="space-y-2">
        {rows.map((s) => (
          <ActionButton
            intent="bare"
            size="none"
            key={s.id}
            onClick={() => onSelect(s.id)}
            className={cn(
              "block w-full whitespace-normal rounded-lg border p-3 text-left font-normal",
              selected === s.id && "border-primary",
            )}
          >
            <b className="block truncate text-sm">{s.title || "Chat"}</b>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <StatusBadge status={s.state} />
              <StatusBadge status="private" label="Private chat" />
            </div>
          </ActionButton>
        ))}
        {rows.length === 0 && (
          <EmptyState
            icon={Bot}
            title="No private conversations"
            description="Your private Charlie chats appear here. Shared incident investigations are kept in the Investigations tab."
            terminal // action is the Charlie drawer
          />
        )}
      </div>
      <div className="rounded-lg border p-4">
        {!selectedConversation ? (
          <EmptyState
            icon={Bot}
            title="Select a conversation"
            description="Only your private user-started conversations can be opened here."
            terminal // action: the conversation list to the left
          />
        ) : h.isLoading ? (
          <LoadingPanel title="Loading private conversation" />
        ) : h.isError ? (
          <QueryFailure label="Conversation" query={h} />
        ) : (
          h.data?.map((m) => (
            <div key={m.id} className="mb-3 rounded-md bg-muted/40 p-3">
              <SafeMarkdown>{m.content}</SafeMarkdown>
              <CharlieMessageParts message={m} />
            </div>
          ))
        )}
      </div>
    </div>
  );
}
