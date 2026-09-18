import { useEffect } from "react";
import { useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import type { CharlieMessage, CharlieSession } from "@/lib/api/charlie";
import { queryKeys } from "@/lib/query-keys";
import { initialCharlieTurnProgress } from "./turn-progress";
import type { CharlieSessionState } from "./charlie-session-state";

/** Reconcile SSE with authoritative transcript/status reads without accepting the preceding turn's terminal cache. */
export function useCharlieSessionReconciliation({
  state,
  history,
  sessionStatus,
  sendPending,
}: {
  state: CharlieSessionState;
  history: UseQueryResult<CharlieMessage[], Error>;
  sessionStatus: UseQueryResult<CharlieSession, Error>;
  sendPending: boolean;
}) {
  const qc = useQueryClient();
  const {
    threadId,
    sessionId,
    viewingThreadId,
    setStreamUnavailable,
    setTurnFailed,
    awaitingReply,
    setAwaitingReply,
    setTurnProgress,
    awaitingReplyRef,
    activeTurnIdRef,
    terminalStatusAfterRef,
    assistantIdsBeforeTurnRef,
  } = state;
  // Reattach UI progress after the drawer was closed during an in-flight turn.
  // Remote active state alone is not enough: a locally active interactive
  // session can have a completed answer. The transcript must also end in an
  // unanswered user (or streaming assistant) message before we show work as
  // pending and disable duplicate sends.
  useEffect(() => {
    if (viewingThreadId || awaitingReply || !sessionId) return;
    const current = sessionStatus.data;
    if (!current || current.id !== sessionId) return;
    if (
      current.state !== "creating" &&
      current.state !== "active" &&
      current.state !== "waiting_approval"
    )
      return;
    if (history.data === undefined && !history.isError) return;
    const lastConversationMessage = [...(history.data ?? [])]
      .reverse()
      .find(
        (message) => message.role === "user" || message.role === "assistant",
      );
    const hasUnansweredTurn =
      lastConversationMessage?.role === "user" ||
      lastConversationMessage?.state === "streaming";
    if (!hasUnansweredTurn) return;

    const now = Date.now();
    awaitingReplyRef.current = true;
    activeTurnIdRef.current = undefined;
    terminalStatusAfterRef.current = sessionStatus.dataUpdatedAt;
    assistantIdsBeforeTurnRef.current = new Set(
      (history.data ?? [])
        .filter((message) => message.role === "assistant")
        .map((message) => message.id),
    );
    setAwaitingReply(true);
    setStreamUnavailable(false);
    setTurnFailed(false);
    setTurnProgress({
      ...initialCharlieTurnProgress(now),
      stage:
        current.state === "waiting_approval" ? "waiting_approval" : "planning",
      label:
        current.state === "waiting_approval"
          ? "Waiting for approval"
          : "Reconnected to active Charlie work",
    });
  }, [
    awaitingReply,
    history.data,
    history.isError,
    sessionId,
    sessionStatus.data,
    sessionStatus.dataUpdatedAt,
    viewingThreadId,
    setStreamUnavailable,
    setTurnFailed,
    setAwaitingReply,
    setTurnProgress,
    awaitingReplyRef,
    activeTurnIdRef,
    terminalStatusAfterRef,
    assistantIdsBeforeTurnRef,
  ]);
  useEffect(() => {
    if (!awaitingReply || !history.data || viewingThreadId) return;
    const response = history.data.find(
      (message) =>
        message.role === "assistant" &&
        !assistantIdsBeforeTurnRef.current.has(message.id),
    );
    if (!response) return;
    // Persisted history is authoritative even when the terminal SSE frame was
    // lost after a proxy/browser reconnect. Never leave a completed response
    // next to an indefinite "Charlie is working" indicator.
    awaitingReplyRef.current = false;
    setAwaitingReply(false);
    setStreamUnavailable(false);
    setTurnProgress((current) =>
      current
        ? {
            ...current,
            stage: "completed",
            label: "Response complete",
            lastEventAt: Date.now(),
          }
        : current,
    );
  }, [
    awaitingReply,
    history.data,
    viewingThreadId,
    setStreamUnavailable,
    setAwaitingReply,
    setTurnProgress,
    awaitingReplyRef,
    assistantIdsBeforeTurnRef,
  ]);
  // Authoritative central-state polling is the final terminal fallback when an
  // SSE connection dies before its terminal event. Successful turns normally
  // stop through SSE or persisted history; failed/aborted turns may have no
  // assistant row, so local-only state is insufficient.
  useEffect(() => {
    if (!awaitingReply || viewingThreadId || sendPending) return;
    const current = sessionStatus.data;
    if (!current || current.id !== sessionId) return;
    if (sessionStatus.dataUpdatedAt <= terminalStatusAfterRef.current) return;
    if (
      current.state !== "completed" &&
      current.state !== "failed" &&
      current.state !== "aborted"
    )
      return;
    awaitingReplyRef.current = false;
    setAwaitingReply(false);
    if (current.state === "failed") setTurnFailed(true);
    if (current.state === "completed") {
      setStreamUnavailable(false);
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.history(sessionId),
      });
      if (threadId) {
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.threadHistory(threadId),
        });
      }
    }
    setTurnProgress((progress) =>
      progress
        ? {
            ...progress,
            stage:
              current.state === "completed"
                ? "completed"
                : current.state === "failed"
                  ? "failed"
                  : "aborted",
            label:
              current.state === "completed"
                ? "Response complete"
                : current.state === "failed"
                  ? "Charlie could not complete the response"
                  : "Turn aborted",
            lastEventAt: Date.now(),
          }
        : progress,
    );
  }, [
    awaitingReply,
    qc,
    sendPending,
    sessionId,
    sessionStatus.data,
    sessionStatus.dataUpdatedAt,
    threadId,
    viewingThreadId,
    setStreamUnavailable,
    setTurnFailed,
    setAwaitingReply,
    setTurnProgress,
    awaitingReplyRef,
    terminalStatusAfterRef,
  ]);
}
