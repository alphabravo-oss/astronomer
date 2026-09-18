import { useState } from "react";
import { useLocation } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/query-keys";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { useCharlie } from "./charlie-context";
import { productModePresentation } from "./charlie-mode";
import { useCharlieConversation } from "./use-charlie-conversation";
import { useCharlieComposer } from "./use-charlie-composer";
import { commandInsertion } from "./commands";
import {
  CharlieHeaderActions,
  CharlieModeSummary,
} from "./charlie-drawer-header";
import { CharlieScope } from "./charlie-drawer-scope";
import { CharlieCommandHelp, CharlieHistory } from "./charlie-drawer-history";
import { CharlieTranscript } from "./charlie-drawer-transcript";
import {
  CharlieComposer,
  CharlieComposerFeedback,
} from "./charlie-drawer-composer";

/** Composition of conversation authority, composer interactions and independently testable drawer views. */
export function CharlieDrawer() {
  const { open, setOpen, resources, remove, add } = useCharlie();
  const pathname = useLocation({ select: (location) => location.pathname });
  const qc = useQueryClient();
  const [conversationListOpen, setConversationListOpen] = useState(false);
  const onNewChat: () => void = () => {
    setConversationListOpen(false);
    composer.resetAfterNewChat();
  };
  const conversation = useCharlieConversation({
    open,
    conversationListOpen,
    resources,
    pathname: window.location.pathname,
    onNewChat,
  });
  const {
    adminMode,
    overview,
    send,
    startNewChat,
    sessionId,
    threadId,
    viewingThreadId,
    abort,
    confirmAbort,
    setConfirmAbort,
    stickToBottomRef,
    setViewingThreadId,
  } = conversation;
  const mode = productModePresentation(
    adminMode.data?.authoritative ?? overview.data?.mode,
  );
  const modeSettling =
    !!adminMode.data &&
    (adminMode.data.requested !== adminMode.data.authoritative ||
      !adminMode.data.workloadCeilingReady ||
      !!adminMode.data.disablePending);
  const composer = useCharlieComposer(conversation, pathname, mode);
  const returnToCurrent = () => {
    setViewingThreadId(undefined);
    stickToBottomRef.current = true;
  };
  const selectConversation = (id: string | undefined) => {
    setViewingThreadId(id);
    setConversationListOpen(false);
    composer.setCommandHelpOpen(false);
    composer.setCommandNotice(undefined);
    stickToBottomRef.current = false;
  };
  const newChat = () => {
    send.reset();
    startNewChat.mutate();
  };
  return (
    <DrawerShell
      title="Charlie"
      subtitle={<CharlieModeSummary mode={mode} modeSettling={modeSettling} />}
      onClose={() => setOpen(false)}
      actions={
        <CharlieHeaderActions
          conversationListOpen={conversationListOpen}
          onToggleHistory={() => setConversationListOpen((value) => !value)}
          onNewChat={newChat}
          newChatPending={startNewChat.isPending}
          canAbort={!!sessionId && !viewingThreadId}
          onAbort={() => setConfirmAbort(true)}
        />
      }
      panelClassName="max-w-xl max-sm:max-w-none"
      bodyClassName="flex min-h-0 flex-col gap-0 overflow-hidden p-0"
    >
      <CharlieScope
        resources={resources}
        remove={remove}
        add={add}
        scopePickerOpen={composer.scopePickerOpen}
        setScopePickerOpen={composer.setScopePickerOpen}
      />
      {conversationListOpen && (
        <CharlieHistory
          threads={conversation.threads}
          threadId={threadId}
          viewingThreadId={viewingThreadId}
          onClose={() => setConversationListOpen(false)}
          onSelect={selectConversation}
        />
      )}
      {composer.commandHelpOpen && (
        <CharlieCommandHelp
          catalogCommands={composer.catalogCommands}
          onClose={() => composer.setCommandHelpOpen(false)}
          onSelect={(command) => {
            composer.setText(commandInsertion(command));
            composer.setCommandHelpOpen(false);
          }}
        />
      )}
      <CharlieTranscript
        messages={conversation.messages}
        catalogCommands={composer.catalogCommands}
        viewingThreadId={viewingThreadId}
        onReturn={returnToCurrent}
        showProgress={conversation.showProgress}
        turnProgress={conversation.turnProgress}
        messagesViewportRef={conversation.messagesViewportRef}
        stickToBottomRef={stickToBottomRef}
        onApprovalChanged={() =>
          void qc.invalidateQueries({
            queryKey: queryKeys.charlie.history(sessionId),
          })
        }
      />
      <div className="shrink-0 space-y-2 border-t border-border bg-background px-5 py-3">
        <CharlieComposerFeedback
          conversation={conversation}
          commandNotice={composer.commandNotice}
        />
        <CharlieComposer
          model={composer}
          sessionId={sessionId}
          viewingThreadId={viewingThreadId}
          historyReady={conversation.historyReady}
          awaitingReply={conversation.awaitingReply}
          send={send}
          onReturn={returnToCurrent}
        />
      </div>
      <ConfirmDialog
        open={confirmAbort}
        onClose={() => setConfirmAbort(false)}
        onConfirm={() => abort.mutate()}
        title="Abort this Charlie session"
        description="Abort revokes this session's product authority. Closing the drawer alone never aborts work."
        confirmText="Abort session"
        confirmValue="ABORT CHARLIE SESSION"
        variant="destructive"
        loading={abort.isPending}
      />
    </DrawerShell>
  );
}
