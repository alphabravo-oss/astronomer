import {
  useMutation,
  useQueryClient,
  type UseQueryResult,
} from "@tanstack/react-query";
import {
  abortCharlieSession,
  newCharlieChat,
  sendCharlieThreadMessage,
  type CharlieMessage,
  type CharlieContextOption,
  type CharlieCommandRequest,
} from "@/lib/api/charlie";
import { queryKeys } from "@/lib/query-keys";
import { initialCharlieTurnProgress } from "./turn-progress";
import type { CharlieSessionState } from "./charlie-session-state";

/** Mutation receipts fence subsequent status reads and preserve the server-owned thread. */
export function useCharlieSessionMutations({
  state,
  history,
  resources,
  pathname,
  onNewChat,
}: {
  state: CharlieSessionState;
  history: UseQueryResult<CharlieMessage[], Error>;
  resources: CharlieContextOption[];
  pathname: string;
  onNewChat: () => void;
}) {
  const qc = useQueryClient();
  const {
    threadId,
    setThreadId,
    sessionId,
    setSessionId,
    setViewingThreadId,
    setLocal,
    setStreamUnavailable,
    setTurnFailed,
    setConfirmAbort,
    setAwaitingReply,
    setStreamGeneration,
    setTurnProgress,
    awaitingReplyRef,
    activeTurnIdRef,
    terminalStatusAfterRef,
    assistantIdsBeforeTurnRef,
    stickToBottomRef,
  } = state;
  const send = useMutation({
    mutationFn: async (input: {
      message: string;
      command?: CharlieCommandRequest;
    }) => {
      // Thread API reattaches a messageable session or continues under the same
      // interactive thread when the prior session is terminal (no blank 409).
      const result = await sendCharlieThreadMessage(input.message, {
        trigger: input.command
          ? `slash_command:${input.command.id}`
          : "user_chat",
        currentUiContext: pathname.slice(0, 255),
        resources: resources.map(({ label: _, summary: __, ...r }) => r),
        command: input.command,
      });
      if (result.thread?.id) {
        setThreadId(result.thread.id);
      }
      const nextSession =
        result.current_session?.id ??
        result.thread?.current_session_id ??
        undefined;
      if (nextSession) {
        setSessionId(nextSession);
      }
      return {
        threadId: result.thread?.id,
        sessionId: nextSession,
        turnId: result.receipt?.turnId,
      };
    },
    onMutate: (input) => {
      awaitingReplyRef.current = true;
      setAwaitingReply(true);
      activeTurnIdRef.current = undefined;
      terminalStatusAfterRef.current = Number.POSITIVE_INFINITY;
      if (sessionId) {
        // Do not let an in-flight read of the preceding turn repopulate a
        // terminal cache while this message is waiting for acceptance.
        void qc.cancelQueries({
          queryKey: queryKeys.charlie.sessionStatus(sessionId),
          exact: true,
        });
      }
      assistantIdsBeforeTurnRef.current = new Set(
        (history.data ?? [])
          .filter((item) => item.role === "assistant")
          .map((item) => item.id),
      );
      setStreamGeneration((value) => value + 1);
      setStreamUnavailable(false);
      setTurnFailed(false);
      setTurnProgress(initialCharlieTurnProgress());
      setLocal((v) => [
        ...v,
        // Prefixed id so optimistic rows are easy to drop once history arrives.
        {
          id: `local:${crypto.randomUUID()}`,
          role: "user",
          content: input.message,
        },
      ]);
    },
    onError: () => {
      awaitingReplyRef.current = false;
      activeTurnIdRef.current = undefined;
      terminalStatusAfterRef.current = 0;
      setAwaitingReply(false);
      setTurnProgress(undefined);
      setTurnFailed(true);
    },
    onSuccess: (ids) => {
      if (ids.turnId) activeTurnIdRef.current = ids.turnId;
      terminalStatusAfterRef.current = 0;
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.activeThread });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.overview });
      if (ids.sessionId) {
        // Drop, rather than refetch over, the prior turn's terminal snapshot.
        // The enabled query below will establish a fresh post-receipt baseline.
        qc.removeQueries({
          queryKey: queryKeys.charlie.sessionStatus(ids.sessionId),
          exact: true,
        });
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.history(ids.sessionId),
        });
      }
      if (ids.threadId) {
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.threadHistory(ids.threadId),
        });
      }
    },
  });
  const startNewChat = useMutation({
    mutationFn: () => newCharlieChat(),
    onSuccess: (result) => {
      setLocal([]);
      awaitingReplyRef.current = false;
      activeTurnIdRef.current = undefined;
      terminalStatusAfterRef.current = 0;
      setAwaitingReply(false);
      setTurnProgress(undefined);
      setStreamUnavailable(false);
      setTurnFailed(false);
      setViewingThreadId(undefined);
      onNewChat();
      setThreadId(result.thread?.id);
      setSessionId(undefined);
      stickToBottomRef.current = true;
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.activeThread });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.threads });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.overview });
    },
  });
  const abort = useMutation({
    mutationFn: () => abortCharlieSession(sessionId!),
    onSuccess: () => {
      setConfirmAbort(false);
      // Keep transcript; only live authority ends. Next send continues the thread.
      awaitingReplyRef.current = false;
      activeTurnIdRef.current = undefined;
      terminalStatusAfterRef.current = 0;
      setAwaitingReply(false);
      setTurnProgress(undefined);
      setTurnFailed(false);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.sessions });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.overview });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.activeThread });
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.history(sessionId),
      });
      if (threadId) {
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.threadHistory(threadId),
        });
      }
    },
  });
  return { send, startNewChat, abort };
}
