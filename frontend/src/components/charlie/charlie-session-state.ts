import { useRef, useState } from "react";
import type { CharlieMessage } from "@/lib/api/charlie";
import type { CharlieTurnProgress } from "./turn-progress";

/** Local turn identity and optimistic transcript state; closing the drawer does not revoke the remote session. */
export function useCharlieSessionState() {
  const [threadId, setThreadId] = useState<string>();
  const [sessionId, setSessionId] = useState<string>();
  const [viewingThreadId, setViewingThreadId] = useState<string>();
  const [local, setLocal] = useState<CharlieMessage[]>([]);
  const [streamUnavailable, setStreamUnavailable] = useState(false);
  const [turnFailed, setTurnFailed] = useState(false);
  const [confirmAbort, setConfirmAbort] = useState(false);
  // True from user send until Charlie produces assistant content or the turn ends.
  const [awaitingReply, setAwaitingReply] = useState(false);
  const awaitingReplyRef = useRef(false);
  const activeTurnIdRef = useRef<string | undefined>(undefined);
  // A terminal session status may be cached from the preceding turn. Only a
  // status fetched after the current message receipt may terminate this turn.
  const terminalStatusAfterRef = useRef(0);
  const assistantIdsBeforeTurnRef = useRef<Set<string>>(new Set());
  const [streamGeneration, setStreamGeneration] = useState(0);
  const [turnProgress, setTurnProgress] = useState<CharlieTurnProgress>();
  const messagesViewportRef = useRef<HTMLDivElement>(null);
  const stickToBottomRef = useRef(true);
  return {
    threadId,
    setThreadId,
    sessionId,
    setSessionId,
    viewingThreadId,
    setViewingThreadId,
    local,
    setLocal,
    streamUnavailable,
    setStreamUnavailable,
    turnFailed,
    setTurnFailed,
    confirmAbort,
    setConfirmAbort,
    awaitingReply,
    setAwaitingReply,
    streamGeneration,
    setStreamGeneration,
    turnProgress,
    setTurnProgress,
    awaitingReplyRef,
    activeTurnIdRef,
    terminalStatusAfterRef,
    assistantIdsBeforeTurnRef,
    messagesViewportRef,
    stickToBottomRef,
  };
}
export type CharlieSessionState = ReturnType<typeof useCharlieSessionState>;
