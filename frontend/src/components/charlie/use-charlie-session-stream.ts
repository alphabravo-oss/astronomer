import {
  useEffect,
  type Dispatch,
  type MutableRefObject,
  type SetStateAction,
} from "react";
import type { QueryClient } from "@tanstack/react-query";
import { queryKeys } from "@/lib/query-keys";
import { subscribeCharlieSessionEvents } from "@/lib/api/charlie";
import {
  charlieProgressEventTurnId,
  initialCharlieTurnProgress,
  updateCharlieTurnProgress,
  type CharlieTurnProgress,
} from "./turn-progress";

export function useCharlieSessionStream({
  sessionId,
  threadId,
  queryClient,
  generation,
  awaitingReplyRef,
  activeTurnIdRef,
  setAwaitingReply,
  setStreamUnavailable,
  setTurnFailed,
  setTurnProgress,
}: {
  sessionId?: string;
  threadId?: string;
  queryClient: QueryClient;
  generation: number;
  awaitingReplyRef: MutableRefObject<boolean>;
  activeTurnIdRef: MutableRefObject<string | undefined>;
  setAwaitingReply: Dispatch<SetStateAction<boolean>>;
  setStreamUnavailable: Dispatch<SetStateAction<boolean>>;
  setTurnFailed: Dispatch<SetStateAction<boolean>>;
  setTurnProgress: Dispatch<SetStateAction<CharlieTurnProgress | undefined>>;
}) {
  useEffect(() => {
    if (!sessionId) return;
    setStreamUnavailable(false);
    let historyRefresh: ReturnType<typeof setTimeout> | undefined;
    const refreshHistory = () => {
      if (historyRefresh !== undefined) clearTimeout(historyRefresh);
      historyRefresh = setTimeout(() => {
        historyRefresh = undefined;
        void queryClient.invalidateQueries({
          queryKey: queryKeys.charlie.history(sessionId),
        });
        if (threadId)
          void queryClient.invalidateQueries({
            queryKey: queryKeys.charlie.threadHistory(threadId),
          });
      }, 750);
    };
    const unsubscribe = subscribeCharlieSessionEvents(
      sessionId,
      (event) => {
        setStreamUnavailable(false);
        refreshHistory();
        if (!awaitingReplyRef.current) return;
        const eventTurnId = charlieProgressEventTurnId(event.data);
        if (
          activeTurnIdRef.current &&
          eventTurnId &&
          eventTurnId !== activeTurnIdRef.current
        )
          return;
        if (
          !activeTurnIdRef.current &&
          eventTurnId &&
          event.type === "turn.started"
        )
          activeTurnIdRef.current = eventTurnId;
        setTurnProgress((current) =>
          updateCharlieTurnProgress(current ?? initialCharlieTurnProgress(), {
            type: event.type,
            data: event.data,
            lastEventId: event.lastEventId,
          }),
        );
        if (
          [
            "turn.completed",
            "turn.failed",
            "turn.aborted",
            "charlie.error",
          ].includes(event.type)
        ) {
          if (event.type === "turn.failed" || event.type === "charlie.error")
            setTurnFailed(true);
          awaitingReplyRef.current = false;
          setAwaitingReply(false);
        }
      },
      () => {
        if (awaitingReplyRef.current) setStreamUnavailable(true);
      },
      () => setStreamUnavailable(false),
    );
    return () => {
      if (historyRefresh !== undefined) clearTimeout(historyRefresh);
      unsubscribe();
    };
  }, [
    activeTurnIdRef,
    awaitingReplyRef,
    generation,
    queryClient,
    sessionId,
    setAwaitingReply,
    setStreamUnavailable,
    setTurnFailed,
    setTurnProgress,
    threadId,
  ]);
}
