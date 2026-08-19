import {
  createContext,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bot,
  Check,
  Command,
  Copy,
  ExternalLink,
  History as HistoryIcon,
  Loader2,
  Plus,
  Search,
  Send,
  Sparkles,
  StopCircle,
  X,
} from "lucide-react";
import { DrawerShell } from "@/components/ui/drawer-shell";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Link } from "@/lib/link";
import { usePathname } from "@/lib/navigation";
import { cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { contextForRoute } from "./context-registry";
import { SafeMarkdown } from "./safe-markdown";
import { CharlieMessageParts } from "./message-parts";
import {
  charlieProgressEventTurnId,
  initialCharlieTurnProgress,
  updateCharlieTurnProgress,
  type CharlieTurnProgress,
} from "./turn-progress";
import {
  abortCharlieSession,
  getCharlieActiveThread,
  getCharlieCommands,
  getCharlieOverview,
  getCharlieSession,
  getCharlieHistory,
  getCharlieThreadHistory,
  listCharlieThreads,
  newCharlieChat,
  searchCharlieContext,
  sendCharlieThreadMessage,
  subscribeCharlieSessionEvents,
  type CharlieContextOption,
  type CharlieCommandRequest,
  type CharlieMessage,
} from "@/lib/api/charlie";
import { getCharlieMode } from "@/lib/api/charlie-admin";
import {
  commandInsertion,
  commandSuggestions,
  contextualCharlieCommands,
  parseCharlieCommand,
} from "./commands";

type CharlieState = {
  open: boolean;
  setOpen: (v: boolean) => void;
  resources: CharlieContextOption[];
  remove: (id: string) => void;
  add: (v: CharlieContextOption) => void;
};

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

/** Truthful, event-driven activity while Charlie is preparing a reply. */
function CharlieProgressIndicator({ progress }: { progress: CharlieTurnProgress }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const elapsedSeconds = Math.max(0, Math.floor((now - progress.startedAt) / 1000));
  const toolCalls = progress.toolCallIds.length;
  const completedTools = progress.completedToolCallIds.length;
  const failedTools = progress.failedToolCallIds.length;
  const blockedTools = progress.blockedToolCallIds.length;
  const pendingTools = Math.max(0, toolCalls - completedTools);
  const quietSeconds = Math.max(0, Math.floor((now - progress.lastEventAt) / 1000));
  const delayed = quietSeconds >= 30;
  const stalled = quietSeconds >= 90;
  let activity = progress.label;
  if (delayed && progress.stage === "analyzing") {
    activity = completedTools > 0
      ? `Waiting for Charlie's model to analyze ${completedTools} tool ${completedTools === 1 ? "result" : "results"}`
      : "Waiting for Charlie's model to analyze the available evidence";
  } else if (delayed && (progress.stage === "queued" || progress.stage === "planning")) {
    activity = "Waiting for Charlie's model to begin the investigation";
  } else if (delayed && progress.stage === "running_tool") {
    activity = progress.capability
      ? `Waiting for ${progress.capability} to return`
      : "Waiting for the diagnostic tool to return";
  }
  return (
    <article
      role="status"
      aria-live="polite"
      aria-label={`Charlie is working: ${activity}`}
      className="mr-8 rounded-lg border bg-card p-3"
      data-testid="charlie-turn-progress"
    >
      <div className="flex items-start gap-2">
        <Loader2 className="mt-0.5 h-4 w-4 shrink-0 animate-spin text-primary motion-reduce:animate-none" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <p className="text-xs font-medium text-muted-foreground">Charlie is working</p>
          <p className="truncate text-sm" title={activity}>{activity}</p>
        </div>
      </div>
      <div
        className="mt-2 h-1.5 overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-label="Charlie request progress"
        aria-valuetext={activity}
      >
        <span
          className="block h-full w-1/3 rounded-full bg-primary motion-reduce:w-2/3"
          style={{ animation: "charlie-progress-slide 1.4s ease-in-out infinite" }}
        />
      </div>
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>{elapsedSeconds}s elapsed</span>
        {toolCalls > 0 ? <span>{completedTools} of {toolCalls} tool {toolCalls === 1 ? "call" : "calls"} finished</span> : null}
        {pendingTools > 0 ? <span>{pendingTools} active or pending</span> : null}
        {failedTools > 0 ? <span>{failedTools} failed</span> : null}
        {blockedTools > 0 ? <span>{blockedTools} safely blocked</span> : null}
        {progress.eventCount > 0 ? (
          <span>{progress.eventCount.toLocaleString()} live {progress.eventCount === 1 ? "update" : "updates"}</span>
        ) : null}
        {delayed ? <span>Last update {quietSeconds}s ago</span> : null}
      </div>
      {stalled ? (
        <p className="mt-2 text-xs text-status-warning">
          This step is taking longer than expected. Charlie will stop it at the configured deadline; you can keep waiting or stop the request.
        </p>
      ) : null}
      <style>{`
        @keyframes charlie-progress-slide {
          0% { transform: translateX(-110%); }
          50% { transform: translateX(110%); }
          100% { transform: translateX(310%); }
        }
        @media (prefers-reduced-motion: reduce) {
          @keyframes charlie-progress-slide {
            0%, 100% { transform: translateX(50%); }
          }
        }
      `}</style>
    </article>
  );
}

function CopyMessageButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  if (!text.trim()) return null;
  return (
    <button
      type="button"
      className="inline-flex items-center gap-1 rounded border px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
      aria-label={copied ? "Copied" : "Copy message"}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        } catch {
          // Fallback for restricted clipboard environments.
          const area = document.createElement("textarea");
          area.value = text;
          area.setAttribute("readonly", "");
          area.style.position = "fixed";
          area.style.left = "-9999px";
          document.body.appendChild(area);
          area.select();
          try {
            document.execCommand("copy");
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          } finally {
            document.body.removeChild(area);
          }
        }
      }}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

const Context = createContext<CharlieState | null>(null);
export const useCharlie = () => {
  const v = useContext(Context);
  if (!v) throw new Error("useCharlie must be inside CharlieShell");
  return v;
};

/** Human-facing product mode badge for chat drawer + hub. */
export const productModeCopy = {
  disabled: {
    key: "disabled" as const,
    label: "Disabled",
    short: "Off",
    ceiling: "No Charlie sessions, triggers, approvals, or actions are allowed.",
    badgeClass:
      "border-status-error/40 bg-status-error/10 text-status-error",
  },
  read_only: {
    key: "read_only" as const,
    label: "Read only",
    short: "Read only",
    ceiling:
      "Investigation and findings only. Charlie cannot change cluster state; write requests become guidance.",
    badgeClass:
      "border-status-info/40 bg-status-info/10 text-status-info",
  },
  approval: {
    key: "approval" as const,
    label: "Approval mode",
    short: "Approval",
    ceiling:
      "Includes read-only. Every exact write is proposed for human review — use the approval card or Approvals tab to confirm.",
    badgeClass:
      "border-status-warning/40 bg-status-warning/10 text-status-warning",
  },
  auto: {
    key: "auto" as const,
    label: "Autonomous",
    short: "Auto",
    ceiling:
      "Includes read-only and approval. Only explicitly allowlisted safe writes may run without a click; other writes still need approval.",
    badgeClass:
      "border-status-success/40 bg-status-success/10 text-status-success",
  },
} as const;

export function productModePresentation(mode: string | undefined) {
  if (mode && mode in productModeCopy) {
    return productModeCopy[mode as keyof typeof productModeCopy];
  }
  return {
    key: "unknown" as const,
    label: "Mode unknown",
    short: "Unknown",
    ceiling:
      "Hard ceiling is unavailable; Charlie must not assume write authority.",
    badgeClass: "border-border bg-muted text-muted-foreground",
  };
}

export function CharlieShell({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const [open, setOpen] = useState(false);
  const [manual, setManual] = useState<CharlieContextOption[]>([]);
  const [removed, setRemoved] = useState<string[]>([]);
  const routeResources = useMemo(() => contextForRoute(pathname), [pathname]);
  const resources = [...routeResources, ...manual]
    .filter((resource) => !removed.includes(`${resource.type}:${resource.id}`))
    .filter(
      (value, index, all) =>
        all.findIndex(
          (candidate) =>
            candidate.type === value.type && candidate.id === value.id,
        ) === index,
    );
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const target = e.target;
      const editing =
        target instanceof HTMLElement &&
        (target.isContentEditable ||
          ["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName));
      if (
        !editing &&
        (e.metaKey || e.ctrlKey) &&
        e.shiftKey &&
        e.key === "."
      ) {
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  const value = {
    open,
    setOpen,
    resources,
    remove: (id: string) => setRemoved((v) => [...v, id]),
    add: (v: CharlieContextOption) => {
      const id = `${v.type}:${v.id}`;
      setRemoved((current) => current.filter((value) => value !== id));
      setManual((current) => [...current, v]);
    },
  };
  return (
    <Context.Provider value={value}>
      {children}
      <button
        onClick={() => setOpen(true)}
        aria-label="Open Charlie assistant"
        aria-expanded={open}
        aria-controls="charlie-assistant-drawer"
        title="Open Charlie (Ctrl/⌘ Shift .)"
        className="fixed bottom-5 right-5 z-40 flex h-12 items-center gap-2 rounded-full bg-primary px-4 text-primary-foreground shadow-lg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none"
      >
        <Sparkles className="h-5 w-5" />
        <span className="hidden sm:inline">Charlie</span>
      </button>
      {open && <CharlieDrawer />}
    </Context.Provider>
  );
}

function ContextPicker({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { add } = useCharlie();
  const [q, setQ] = useState("");
  const result = useQuery({
    queryKey: queryKeys.charlie.contextSearch(q),
    queryFn: () => searchCharlieContext(q),
    enabled: open && (q.trim().length === 0 || q.trim().length >= 2),
    retry: false,
  });
  if (!open)
    return (
      <button
        type="button"
        onClick={() => onOpenChange(true)}
        aria-expanded="false"
        className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs"
      >
        <Plus className="h-3 w-3" />
        Narrow scope
      </button>
    );
  return (
    <div className="min-w-72 rounded-md border bg-background p-2 shadow-sm" role="search">
      <div className="mb-2 flex items-center justify-between gap-2">
        <p className="text-xs font-medium">Choose a diagnostic scope</p>
        <button
          type="button"
          onClick={() => onOpenChange(false)}
          className="rounded px-1.5 py-0.5 text-xs text-muted-foreground hover:bg-accent"
        >
          Done
        </button>
      </div>
      <label className="flex items-center gap-2">
        <Search className="h-4 w-4" />
        <span className="sr-only">Search components or agent connections</span>
        <input
          aria-label="Search components or agent connections"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search components or agent connections"
          className="w-full bg-transparent text-sm outline-none"
        />
      </label>
      {result.isError && (
        <p role="status" className="mt-2 text-xs text-muted-foreground">
          Context search is unavailable for this installation.
        </p>
      )}
      {result.isLoading && (
        <p role="status" className="mt-2 text-xs text-muted-foreground">
          Loading available scopes…
        </p>
      )}
      {result.data?.map((v) => (
        <button
          type="button"
          key={`${v.type}:${v.id}`}
          onClick={() => {
            add(v);
            onOpenChange(false);
          }}
          className="mt-2 block w-full rounded p-2 text-left text-sm hover:bg-accent"
        >
          <b>{v.label}</b>
          <span className="block text-xs text-muted-foreground">
            {v.summary}
          </span>
        </button>
      ))}
      {!result.isLoading && result.data?.length === 0 && (
        <p className="mt-2 text-xs text-muted-foreground">
          No matching scope is available to your account.
        </p>
      )}
    </div>
  );
}

function CharlieDrawer() {
  const { open, setOpen, resources, remove } = useCharlie();
  const pathname = usePathname();
  const qc = useQueryClient();
  const [threadId, setThreadId] = useState<string>();
  const [sessionId, setSessionId] = useState<string>();
  const [viewingThreadId, setViewingThreadId] = useState<string>();
  const [text, setText] = useState("");
  const [local, setLocal] = useState<CharlieMessage[]>([]);
  const [scopePickerOpen, setScopePickerOpen] = useState(false);
  const [commandHelpOpen, setCommandHelpOpen] = useState(false);
  const [conversationListOpen, setConversationListOpen] = useState(false);
  const [commandNotice, setCommandNotice] = useState<string>();
  const [selectedCommandIndex, setSelectedCommandIndex] = useState(0);
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
    queryFn: getCharlieMode,
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
    const current = activeThread.data?.current_session?.id
      ?? thread.current_session_id
      ?? undefined;
    if (current) {
      setSessionId(current);
    }
  }, [activeThread.data]);
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
  const catalogCommands = useMemo(
    () => commands.data?.commands ?? [],
    [commands.data?.commands],
  );
  const slashSuggestions = useMemo(
    () => commandSuggestions(text, catalogCommands),
    [catalogCommands, text],
  );
  const suggestedCommands = useMemo(
    () => contextualCharlieCommands(pathname, catalogCommands),
    [catalogCommands, pathname],
  );
  useEffect(() => setSelectedCommandIndex(0), [text]);
  useEffect(() => {
    if (!sessionId) return;
    setStreamUnavailable(false);
    let historyRefresh: ReturnType<typeof setTimeout> | undefined;
    const scheduleHistoryRefresh = () => {
      if (historyRefresh !== undefined) clearTimeout(historyRefresh);
      historyRefresh = setTimeout(() => {
        historyRefresh = undefined;
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.history(sessionId),
        });
        if (threadId) {
          void qc.invalidateQueries({
            queryKey: queryKeys.charlie.threadHistory(threadId),
          });
        }
      }, 750);
    };
    const unsubscribe = subscribeCharlieSessionEvents(
      sessionId,
      (event) => {
        setStreamUnavailable(false);
        scheduleHistoryRefresh();
        if (!awaitingReplyRef.current) return;
        const eventTurnId = charlieProgressEventTurnId(event.data);
        const expectedTurnId = activeTurnIdRef.current;
        if (expectedTurnId && eventTurnId && eventTurnId !== expectedTurnId) {
          return;
        }
        if (!expectedTurnId && eventTurnId && event.type === "turn.started") {
          activeTurnIdRef.current = eventTurnId;
        }
        setTurnProgress((current) =>
          updateCharlieTurnProgress(
            current ?? initialCharlieTurnProgress(),
            {
              type: event.type,
              data: event.data,
              lastEventId: event.lastEventId,
            },
          ),
        );
        // Stop the progress indicator once the turn finishes (or hard-errors).
        if (
          event.type === "turn.completed" ||
          event.type === "turn.failed" ||
          event.type === "turn.aborted" ||
          event.type === "charlie.error"
        ) {
          if (event.type === "turn.failed" || event.type === "charlie.error") {
            setTurnFailed(true);
          }
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
  }, [qc, sessionId, streamGeneration, threadId]);
  const send = useMutation({
    mutationFn: async (input: { message: string; command?: CharlieCommandRequest }) => {
      // Thread API reattaches a messageable session or continues under the same
      // interactive thread when the prior session is terminal (no blank 409).
      const result = await sendCharlieThreadMessage(input.message, {
        trigger: input.command ? `slash_command:${input.command.id}` : "user_chat",
        currentUiContext: location.pathname.slice(0, 255),
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
        { id: `local:${crypto.randomUUID()}`, role: "user", content: input.message },
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
  const startNewChat = useMutation({
    mutationFn: newCharlieChat,
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
      setConversationListOpen(false);
      setCommandHelpOpen(false);
      setCommandNotice(undefined);
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
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.history(sessionId) });
      if (threadId) {
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.threadHistory(threadId),
        });
      }
    },
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
    ) return;
    if (history.data === undefined && !history.isError) return;
    const lastConversationMessage = [...(history.data ?? [])]
      .reverse()
      .find((message) => message.role === "user" || message.role === "assistant");
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
      stage: current.state === "waiting_approval" ? "waiting_approval" : "planning",
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
  }, [awaitingReply, history.data, viewingThreadId]);
  // Authoritative central-state polling is the final terminal fallback when an
  // SSE connection dies before its terminal event. Successful turns normally
  // stop through SSE or persisted history; failed/aborted turns may have no
  // assistant row, so local-only state is insufficient.
  useEffect(() => {
    if (!awaitingReply || viewingThreadId || send.isPending) return;
    const current = sessionStatus.data;
    if (!current || current.id !== sessionId) return;
    if (sessionStatus.dataUpdatedAt <= terminalStatusAfterRef.current) return;
    if (
      current.state !== "completed" &&
      current.state !== "failed" &&
      current.state !== "aborted"
    ) return;
    awaitingReplyRef.current = false;
    setAwaitingReply(false);
    if (current.state === "failed") setTurnFailed(true);
    if (current.state === "completed") {
      setStreamUnavailable(false);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.history(sessionId) });
      if (threadId) {
        void qc.invalidateQueries({ queryKey: queryKeys.charlie.threadHistory(threadId) });
      }
    }
    setTurnProgress((progress) => progress
      ? {
          ...progress,
          stage: current.state === "completed" ? "completed" : current.state === "failed" ? "failed" : "aborted",
          label: current.state === "completed" ? "Response complete" : current.state === "failed" ? "Charlie could not complete the response" : "Turn aborted",
          lastEventAt: Date.now(),
        }
      : progress);
  }, [awaitingReply, qc, send.isPending, sessionId, sessionStatus.data, sessionStatus.dataUpdatedAt, threadId, viewingThreadId]);
  const showProgress = !viewingThreadId && (send.isPending || awaitingReply) && !send.isError;
  const historyReady =
    (!displayedThreadId && !sessionId) || history.data !== undefined;
  // Keep the latest turn visible above the fixed composer unless the user has
  // scrolled up to read earlier history.
  useLayoutEffect(() => {
    const el = messagesViewportRef.current;
    if (!el || !stickToBottomRef.current) return;
    el.scrollTop = el.scrollHeight;
  }, [messages, showProgress, streamUnavailable, send.isError, history.isError]);
  const mode = productModePresentation(
    adminMode.data?.authoritative ?? overview.data?.mode,
  );
  const modeSettling =
    !!adminMode.data &&
    (adminMode.data.requested !== adminMode.data.authoritative ||
      !adminMode.data.workloadCeilingReady ||
      !!adminMode.data.disablePending);
  const submitComposer = () => {
    const value = text.trim();
    if (!value || viewingThreadId || !historyReady || send.isPending || awaitingReply) return;
    if (value.startsWith("/")) {
      const parsed = parseCharlieCommand(value, catalogCommands);
      if (!parsed) {
        setCommandNotice(commands.isError
          ? "The command catalog is unavailable. Natural-language chat is still available."
          : "Unknown or incomplete command. Choose a suggestion or use /help.");
        return;
      }
      if (parsed.descriptor.execution === "client") {
        setText("");
        setCommandNotice(undefined);
        switch (parsed.descriptor.id) {
          case "help":
            setCommandHelpOpen(true);
            break;
          case "scope":
            setScopePickerOpen(true);
            break;
          case "mode":
            setCommandNotice(`Charlie is in ${mode.label}. ${mode.ceiling}`);
            break;
          case "new":
            send.reset();
            startNewChat.mutate();
            break;
          case "stop":
            if (sessionId) setConfirmAbort(true);
            else setCommandNotice("There is no active Charlie session to stop.");
            break;
        }
        return;
      }
      setText("");
      setCommandNotice(undefined);
      stickToBottomRef.current = true;
      send.mutate({ message: value, command: parsed.request });
      return;
    }
    setText("");
    setCommandNotice(undefined);
    stickToBottomRef.current = true;
    send.mutate({ message: value });
  };
  return (
    <DrawerShell
      title="Charlie"
      subtitle={
        <span className="flex flex-wrap items-center gap-2">
          <span>AI assistance within your authorized Astronomer scope</span>
          <span
            className={cn(
              "inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs font-semibold",
              modeSettling
                ? "border-status-info/40 bg-status-info/10 text-status-info"
                : mode.badgeClass,
            )}
            aria-label={
              modeSettling
                ? `Charlie mode changing: ${mode.label}`
                : `Current Charlie mode: ${mode.label}`
            }
            data-testid="charlie-mode-badge"
            data-mode={mode.key}
            data-settling={modeSettling ? "true" : "false"}
          >
            {modeSettling ? (
              <Loader2 className="h-3 w-3 animate-spin motion-reduce:animate-none" />
            ) : null}
            Mode: {mode.label}
            {modeSettling ? " · settling agents" : ""}
          </span>
          <span className="basis-full text-xs text-muted-foreground">
            {modeSettling
              ? "Mode ceiling is rolling out. Wait until both product-agent replicas are verified before relying on writes."
              : mode.ceiling}
          </span>
        </span>
      }
      onClose={() => setOpen(false)}
      actions={
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => setConversationListOpen((value) => !value)}
            aria-expanded={conversationListOpen}
            className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs"
          >
            <HistoryIcon className="h-3 w-3" /> History
          </button>
          <button
            type="button"
            onClick={() => {
              send.reset();
              startNewChat.mutate();
            }}
            disabled={startNewChat.isPending}
            className="rounded-md border px-2 py-1 text-xs"
          >
            New chat
          </button>
          {sessionId && !viewingThreadId && (
            <button
              type="button"
              onClick={() => setConfirmAbort(true)}
              className="inline-flex items-center gap-1 rounded-md border border-status-error/40 px-2 py-1 text-xs text-status-error"
            >
              <StopCircle className="h-3 w-3" /> Abort turn
            </button>
          )}
        </div>
      }
      panelClassName="max-w-xl max-sm:max-w-none"
      // Chat layout: fixed composer at the bottom; only the transcript scrolls.
      bodyClassName="flex min-h-0 flex-col gap-0 overflow-hidden p-0"
    >
      <div
        id="charlie-assistant-drawer"
        className="shrink-0 space-y-2 border-b border-border px-5 py-3"
      >
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0 flex-1">
            <p className="mb-1 text-xs font-medium text-muted-foreground">Scope</p>
            <div className="flex flex-wrap gap-2" role="list" aria-label="Conversation scope">
              {resources.length === 0 ? (
                <span
                  role="listitem"
                  className="inline-flex items-center rounded-full bg-muted px-2 py-1 text-xs"
                >
                  This Astronomer deployment
                </span>
              ) : null}
              {resources.map((r) => (
                <span
                  key={`${r.type}:${r.id}`}
                  role="listitem"
                  aria-label={`${r.label}: ${r.summary}`}
                  className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-1 text-xs"
                >
                  {r.label}
                  <button
                    type="button"
                    aria-label={`Remove ${r.label}`}
                    onClick={() => remove(`${r.type}:${r.id}`)}
                  >
                    <X className="h-3 w-3" />
                  </button>
                </span>
              ))}
            </div>
          </div>
          <div className="shrink-0">
            <ContextPicker open={scopePickerOpen} onOpenChange={setScopePickerOpen} />
          </div>
        </div>
        <p className="text-xs text-muted-foreground">
          Charlie retrieves authorized diagnostics through audited read tools
          when needed. Choose a component or agent connection to narrow this
          conversation.
        </p>
      </div>
      {conversationListOpen && (
        <section className="max-h-52 shrink-0 overflow-y-auto border-b border-border bg-card px-5 py-3" aria-label="Recent Charlie conversations">
          <div className="mb-2 flex items-center justify-between gap-2">
            <p className="text-xs font-semibold">Recent conversations</p>
            <button type="button" aria-label="Close conversation history" onClick={() => setConversationListOpen(false)} className="text-xs text-muted-foreground">Close</button>
          </div>
          {threads.isLoading ? <p className="text-xs text-muted-foreground">Loading conversations…</p> : null}
          {threads.isError ? <p role="alert" className="text-xs text-muted-foreground">Conversation history is unavailable.</p> : null}
          <div className="space-y-1">
            {threads.data?.map((thread) => {
              const current = thread.id === threadId;
              const selected = current ? !viewingThreadId : thread.id === viewingThreadId;
              return (
                <button
                  type="button"
                  key={thread.id}
                  aria-current={selected ? "true" : undefined}
                  onClick={() => {
                    setViewingThreadId(current ? undefined : thread.id);
                    setConversationListOpen(false);
                    setCommandHelpOpen(false);
                    setCommandNotice(undefined);
                    stickToBottomRef.current = false;
                  }}
                  className={cn("block w-full rounded-md border px-3 py-2 text-left hover:bg-accent", selected && "border-primary bg-primary/5")}
                >
                  <span className="block truncate text-sm font-medium">{thread.title || "Untitled conversation"}</span>
                  <span className="block text-[11px] text-muted-foreground">
                    {current ? "Current" : thread.state === "archived" ? "Previous" : thread.state}
                    {thread.updated_at ? ` · ${new Date(thread.updated_at).toLocaleString()}` : ""}
                  </span>
                </button>
              );
            })}
          </div>
          {!threads.isLoading && threads.data?.length === 0 ? <p className="text-xs text-muted-foreground">No previous conversations yet.</p> : null}
        </section>
      )}
      {commandHelpOpen && (
        <section className="max-h-64 shrink-0 overflow-y-auto border-b border-border bg-card px-5 py-3" aria-label="Charlie command help">
          <div className="mb-2 flex items-center justify-between gap-2">
            <div>
              <p className="text-sm font-semibold">Charlie commands</p>
              <p className="text-xs text-muted-foreground">Shortcuts use the same scope, mode, approvals, and audit controls as ordinary chat.</p>
            </div>
            <button type="button" aria-label="Close command help" onClick={() => setCommandHelpOpen(false)} className="text-xs text-muted-foreground">Close</button>
          </div>
          <div className="space-y-1">
            {catalogCommands.map((command) => (
              <button
                type="button"
                key={command.id}
                onClick={() => {
                  setText(commandInsertion(command));
                  setCommandHelpOpen(false);
                }}
                className="block w-full rounded-md px-2 py-1.5 text-left hover:bg-accent"
              >
                <span className="font-mono text-xs">/{command.name}{command.argument ? ` <${command.argument.placeholder}>` : ""}</span>
                <span className="ml-2 text-xs text-muted-foreground">{command.description}</span>
              </button>
            ))}
          </div>
        </section>
      )}
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
          <div className="flex items-center justify-between gap-3 rounded-md border bg-muted/50 p-3" role="status">
            <div>
              <p className="text-sm font-medium">Viewing a previous conversation</p>
              <p className="text-xs text-muted-foreground">This transcript is read-only and is not added to your current Charlie context.</p>
            </div>
            <button type="button" onClick={() => { setViewingThreadId(undefined); stickToBottomRef.current = true; }} className="shrink-0 rounded-md border px-2 py-1 text-xs">Back to current</button>
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
                    m.role === "user" ? "Message from you" : "Message from Charlie"
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
                    onApprovalChanged={() =>
                      void qc.invalidateQueries({
                        queryKey: queryKeys.charlie.history(sessionId),
                      })
                    }
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
      <div className="shrink-0 space-y-2 border-t border-border bg-background px-5 py-3">
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
                  className="rounded border px-2 py-1 text-xs"
                >
                  Reconnect and retry history
                </button>
              )}
              {(send.isError || turnFailed) && send.variables && (
                <button
                  type="button"
                  onClick={() => send.mutate(send.variables!)}
                  className="rounded border px-2 py-1 text-xs"
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
          <p role="alert" className="rounded-md border border-status-error/40 p-2 text-xs text-status-error">
            Abort is pending or could not be confirmed. The product-side session remains locally closed.
          </p>
        )}
        {commandNotice ? <p role="status" className="rounded-md border bg-muted/50 p-2 text-xs">{commandNotice}</p> : null}
        {viewingThreadId ? (
          <div className="flex items-center justify-between gap-3 rounded-md border p-3">
            <p className="text-xs text-muted-foreground">Previous conversations are read-only. Return to the current conversation to message Charlie.</p>
            <button type="button" onClick={() => { setViewingThreadId(undefined); stickToBottomRef.current = true; }} className="shrink-0 rounded-md bg-primary px-3 py-2 text-xs text-primary-foreground">Return to current</button>
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
              <div className="flex flex-wrap gap-1.5" aria-label="Suggested Charlie commands">
                {suggestedCommands.map((command) => (
                  <button type="button" key={command.id} onClick={() => setText(commandInsertion(command))} className="rounded-full border px-2 py-1 font-mono text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground">
                    /{command.name}
                  </button>
                ))}
              </div>
            ) : null}
            {text.trimStart().startsWith("/") && slashSuggestions.length > 0 ? (
              <div id="charlie-command-suggestions" role="listbox" aria-label="Charlie command suggestions" className="max-h-56 overflow-y-auto rounded-md border bg-popover p-1 shadow-md">
                {slashSuggestions.map((command, index) => (
                  <button
                    type="button"
                    role="option"
                    aria-selected={index === selectedCommandIndex}
                    key={command.id}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => setText(commandInsertion(command))}
                    className={cn("flex w-full items-start gap-2 rounded px-2 py-2 text-left", index === selectedCommandIndex ? "bg-accent" : "hover:bg-accent")}
                  >
                    <Command className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" />
                    <span className="min-w-0">
                      <span className="block font-mono text-xs">/{command.name}{command.argument ? ` <${command.argument.placeholder}>` : ""}</span>
                      <span className="block text-[11px] text-muted-foreground">{command.description}</span>
                    </span>
                  </button>
                ))}
              </div>
            ) : null}
            <textarea
              aria-label="Message Charlie"
              aria-controls={slashSuggestions.length ? "charlie-command-suggestions" : undefined}
              value={text}
              onChange={(e) => { setText(e.target.value); setCommandNotice(undefined); }}
              onKeyDown={(e) => {
                if (e.nativeEvent.isComposing) return;
                if (slashSuggestions.length > 0 && (e.key === "ArrowDown" || e.key === "ArrowUp")) {
                  e.preventDefault();
                  setSelectedCommandIndex((current) => e.key === "ArrowDown"
                    ? (current + 1) % slashSuggestions.length
                    : (current - 1 + slashSuggestions.length) % slashSuggestions.length);
                  return;
                }
                if (slashSuggestions.length > 0 && e.key === "Tab") {
                  e.preventDefault();
                  setText(commandInsertion(slashSuggestions[selectedCommandIndex] ?? slashSuggestions[0]));
                  return;
                }
                // Enter sends a complete command/message; for a partial command
                // it accepts the highlighted suggestion. Shift+Enter is newline.
                if (e.key !== "Enter" || e.shiftKey) return;
                e.preventDefault();
                if (slashSuggestions.length > 0 && !parseCharlieCommand(text, catalogCommands)) {
                  setText(commandInsertion(slashSuggestions[selectedCommandIndex] ?? slashSuggestions[0]));
                  return;
                }
                submitComposer();
              }}
              rows={3}
              maxLength={sessionId ? 32768 : 4096}
              className="w-full resize-none rounded-lg border bg-background p-3 text-sm"
              placeholder="Ask Charlie or type / for commands…"
            />
            <div className="flex justify-between">
              <Link
                href={`/dashboard/charlie?tab=conversations${sessionId ? `&session=${sessionId}` : ""}`}
                className="inline-flex items-center gap-1 text-xs text-primary"
              >
                Open Charlie hub
                <ExternalLink className="h-3 w-3" />
              </Link>
              <button
                type="submit"
                disabled={!text.trim() || !historyReady || send.isPending || awaitingReply}
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
