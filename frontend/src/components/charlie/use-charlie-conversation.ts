import { useEffect, useLayoutEffect, useMemo } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getCharlieOverview,
  getCharlieActiveThread,
  getCharlieCommands,
  listCharlieThreads,
  getCharlieThreadHistory,
  getCharlieHistory,
  getCharlieSession,
  type CharlieContextOption,
} from "@/lib/api/charlie";
import { getCharlieMode } from "@/lib/api/charlie-admin";
import { queryKeys } from "@/lib/query-keys";
import { useCharlieSessionState } from "./charlie-session-state";
import { useCharlieSessionMutations } from "./use-charlie-session-mutations";
import { useCharlieSessionStream } from "./use-charlie-session-stream";
import { useCharlieSessionReconciliation } from "./use-charlie-session-reconciliation";

/** Server-owned conversation queries and the coordinated lifecycle of one interactive turn. */
export function useCharlieConversation({
  open,
  conversationListOpen,
  resources,
  pathname,
  onNewChat,
}: {
  open: boolean;
  conversationListOpen: boolean;
  resources: CharlieContextOption[];
  pathname: string;
  onNewChat: () => void;
}) {
  const state = useCharlieSessionState();
  const qc = useQueryClient();
  const {
    threadId,
    setThreadId,
    sessionId,
    setSessionId,
    viewingThreadId,
    local,
    streamUnavailable,
    setStreamUnavailable,
    setTurnFailed,
    awaitingReply,
    setAwaitingReply,
    streamGeneration,
    setTurnProgress,
    awaitingReplyRef,
    activeTurnIdRef,
    messagesViewportRef,
    stickToBottomRef,
  } = state;
  // Server-owned active interactive thread survives close/reopen. Close only
  // hides the drawer; New chat is the explicit reset control.
  const overview = useQuery({
    queryKey: queryKeys.charlie.overview,
    queryFn: getCharlieOverview,
    retry: false,
  });
  // Optional admin mode status: when available, show ceiling settle state on the badge.
  const adminMode = useQuery({
    queryKey: queryKeys.charlie.adminMode,
    queryFn: ({ signal }) => getCharlieMode(signal),
    retry: false,
    enabled: open,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (!data) return false;
      if (
        data.requested !== data.authoritative ||
        !data.workloadCeilingReady ||
        data.disablePending
      ) {
        return 2500;
      }
      return false;
    },
  });
  const activeThread = useQuery({
    queryKey: queryKeys.charlie.activeThread,
    queryFn: getCharlieActiveThread,
    retry: false,
    refetchInterval: awaitingReply && !viewingThreadId ? 1_500 : false,
  });
  const commands = useQuery({
    queryKey: queryKeys.charlie.commands,
    queryFn: getCharlieCommands,
    enabled: open,
    retry: false,
  });
  const threads = useQuery({
    queryKey: queryKeys.charlie.threads,
    queryFn: listCharlieThreads,
    enabled: open && conversationListOpen,
    retry: false,
  });
  useEffect(() => {
    const thread = activeThread.data?.thread;
    if (!thread?.id) {
      return;
    }
    setThreadId(thread.id);
    const current =
      activeThread.data?.current_session?.id ??
      thread.current_session_id ??
      undefined;
    if (current) {
      setSessionId(current);
    }
  }, [activeThread.data, setThreadId, setSessionId]);
  const displayedThreadId = viewingThreadId ?? threadId;
  const history = useQuery({
    queryKey: displayedThreadId
      ? queryKeys.charlie.threadHistory(displayedThreadId)
      : queryKeys.charlie.history(sessionId),
    queryFn: () =>
      displayedThreadId
        ? getCharlieThreadHistory(displayedThreadId)
        : getCharlieHistory(sessionId!),
    enabled: !!displayedThreadId || !!sessionId,
    retry: false,
    // SSE remains the low-latency path. Polling while one turn is outstanding
    // is the authoritative fallback when a browser/proxy misses a terminal
    // frame after Charlie has already persisted the assistant response.
    refetchInterval: awaitingReply && !viewingThreadId ? 1_500 : false,
  });
  const { send, startNewChat, abort } = useCharlieSessionMutations({
    state,
    history,
    resources,
    pathname,
    onNewChat,
  });
  const sessionStatus = useQuery({
    queryKey: queryKeys.charlie.sessionStatus(sessionId),
    queryFn: () => getCharlieSession(sessionId!),
    // Always reconcile the current server-owned session when the drawer opens.
    // Closing the drawer intentionally unmounts the UI and its EventSource, but
    // it does not abort Charlie. This status read lets a reopened drawer attach
    // to work that continued while it was hidden. While a new message is being
    // accepted, the previous turn's status must remain out of consideration.
    enabled: !viewingThreadId && !!sessionId && !send.isPending,
    retry: false,
    // The first read detects work that survived a closed drawer. Once that
    // work is reattached, awaitingReply enables terminal-state polling.
    refetchInterval: awaitingReply && !send.isPending ? 1_500 : false,
  });
  useCharlieSessionStream({
    sessionId,
    threadId,
    queryClient: qc,
    generation: streamGeneration,
    awaitingReplyRef,
    activeTurnIdRef,
    setAwaitingReply,
    setStreamUnavailable,
    setTurnFailed,
    setTurnProgress,
  });
  useCharlieSessionReconciliation({
    state,
    history,
    sessionStatus,
    sendPending: send.isPending,
  });
  // Optimistic local user rows use random ids; history returns server item ids.
  // Drop local user bubbles once the same content appears in history so "hi"
  // does not render twice.
  const messages = useMemo(() => {
    const historyMessages = history.data ?? [];
    if (viewingThreadId) return historyMessages;
    const historyUserContents = new Set(
      historyMessages
        .filter((m) => m.role === "user")
        .map((m) => m.content.trim()),
    );
    const optimistic = local.filter(
      (m) =>
        m.role !== "user" ||
        !historyUserContents.has(m.content.trim()) ||
        // Keep the optimistic row until history has loaded at least one user turn.
        historyMessages.length === 0,
    );
    // Prefer server history order; optimistic only when not yet confirmed.
    return [
      ...historyMessages,
      ...optimistic.filter((m) => !historyMessages.some((h) => h.id === m.id)),
    ];
  }, [history.data, local, viewingThreadId]);
  const showProgress =
    !viewingThreadId && (send.isPending || awaitingReply) && !send.isError;
  const historyReady =
    (!displayedThreadId && !sessionId) || history.data !== undefined;
  // Keep the latest turn visible above the fixed composer unless the user has
  // scrolled up to read earlier history.
  useLayoutEffect(() => {
    const el = messagesViewportRef.current;
    if (!el || !stickToBottomRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [
    messages,
    showProgress,
    streamUnavailable,
    send.isError,
    history.isError,
    messagesViewportRef,
    stickToBottomRef,
  ]);
  return {
    ...state,
    overview,
    adminMode,
    commands,
    threads,
    history,
    send,
    startNewChat,
    abort,
    messages,
    showProgress,
    historyReady,
  };
}
export type CharlieConversation = ReturnType<typeof useCharlieConversation>;
