import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Bot,
  ExternalLink,
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
  abortCharlieSession,
  createCharlieSession,
  getCharlieOverview,
  getCharlieHistory,
  searchCharlieContext,
  sendCharlieMessage,
  subscribeCharlieSessionEvents,
  type CharlieContextOption,
  type CharlieMessage,
} from "@/lib/api/charlie";

type CharlieState = {
  open: boolean;
  setOpen: (v: boolean) => void;
  resources: CharlieContextOption[];
  remove: (id: string) => void;
  add: (v: CharlieContextOption) => void;
};
const Context = createContext<CharlieState | null>(null);
export const useCharlie = () => {
  const v = useContext(Context);
  if (!v) throw new Error("useCharlie must be inside CharlieShell");
  return v;
};

const productModeCopy = {
  disabled: {
    label: "disabled",
    ceiling: "Hard ceiling: no Charlie sessions, triggers, approvals, or actions are allowed.",
  },
  read_only: {
    label: "read_only",
    ceiling: "Hard ceiling: authorized investigation and findings only; Charlie cannot write.",
  },
  approval: {
    label: "approval_required",
    ceiling: "Hard ceiling: includes read_only; every exact write still requires current human approval.",
  },
  auto: {
    label: "automation",
    ceiling: "Hard ceiling: includes read_only and approval_required; only explicitly allowed safe writes may run automatically.",
  },
} as const;

function productModePresentation(mode: string | undefined) {
  return productModeCopy[mode as keyof typeof productModeCopy] ?? {
    label: "unknown",
    ceiling: "Hard ceiling is unavailable; Charlie must not assume write authority.",
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

function ContextPicker() {
  const { add } = useCharlie();
  const [open, setOpen] = useState(false);
  const [q, setQ] = useState("");
  const result = useQuery({
    queryKey: queryKeys.charlie.contextSearch(q),
    queryFn: () => searchCharlieContext(q),
    enabled: open && q.trim().length >= 2,
    retry: false,
  });
  if (!open)
    return (
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-expanded="false"
        className="inline-flex items-center gap-1 rounded-md border px-2 py-1 text-xs"
      >
        <Plus className="h-3 w-3" />
        Add context
      </button>
    );
  return (
    <div className="rounded-md border p-2" role="search">
      <label className="flex items-center gap-2">
        <Search className="h-4 w-4" />
        <span className="sr-only">Search authorized context</span>
        <input
          aria-label="Search authorized context"
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search authorized resources"
          className="w-full bg-transparent text-sm outline-none"
        />
      </label>
      {result.isError && (
        <p role="status" className="mt-2 text-xs text-muted-foreground">
          Context search is unavailable for this installation.
        </p>
      )}
      {result.data?.map((v) => (
        <button
          type="button"
          key={`${v.type}:${v.id}`}
          onClick={() => {
            add(v);
            setOpen(false);
          }}
          className="mt-2 block w-full rounded p-2 text-left text-sm hover:bg-accent"
        >
          <b>{v.label}</b>
          <span className="block text-xs text-muted-foreground">
            {v.summary}
          </span>
        </button>
      ))}
    </div>
  );
}

function CharlieDrawer() {
  const { setOpen, resources, remove } = useCharlie();
  const qc = useQueryClient();
  const [sessionId, setSessionId] = useState<string>();
  const [text, setText] = useState("");
  const [local, setLocal] = useState<CharlieMessage[]>([]);
  const [streamUnavailable, setStreamUnavailable] = useState(false);
  const [restoreAttempted, setRestoreAttempted] = useState(false);
  const [confirmAbort, setConfirmAbort] = useState(false);
  const overview = useQuery({
    queryKey: queryKeys.charlie.overview,
    queryFn: getCharlieOverview,
    retry: false,
  });
  useEffect(() => {
    if (restoreAttempted || overview.isLoading) return;
    setRestoreAttempted(true);
    const latest = overview.data?.sessions.find(
      (session) =>
        session.visibility === "private" &&
        session.source === "user" &&
        !["aborted", "failed"].includes(session.state),
    );
    if (latest) setSessionId(latest.id);
  }, [overview.data, overview.isLoading, restoreAttempted]);
  const history = useQuery({
    queryKey: queryKeys.charlie.history(sessionId),
    queryFn: () => getCharlieHistory(sessionId!),
    enabled: !!sessionId,
    retry: false,
  });
  useEffect(() => {
    if (!sessionId) return;
    setStreamUnavailable(false);
    return subscribeCharlieSessionEvents(
      sessionId,
      () => {
        setStreamUnavailable(false);
        void qc.invalidateQueries({
          queryKey: queryKeys.charlie.history(sessionId),
        });
      },
      () => setStreamUnavailable(true),
    );
  }, [qc, sessionId]);
  const send = useMutation({
    mutationFn: async (message: string) => {
      let id = sessionId;
      if (!id) {
        const s = await createCharlieSession({
          clientSessionId: crypto.randomUUID(),
          intent: message,
          trigger: "user_chat",
          currentUiContext: location.pathname.slice(0, 255),
          resources: resources.map(({ label: _, summary: __, ...r }) => r),
        });
        id = s.id;
        setSessionId(id);
      } else {
        // Charlie executes the initial session intent as the first turn. Do
        // not enqueue the same user message a second time for a new session.
        await sendCharlieMessage(id, message);
      }
      return id;
    },
    onMutate: (message) =>
      setLocal((v) => [
        ...v,
        { id: crypto.randomUUID(), role: "user", content: message },
      ]),
    onSuccess: (id) =>
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.history(id) }),
  });
  const abort = useMutation({
    mutationFn: () => abortCharlieSession(sessionId!),
    onSuccess: () => {
      setConfirmAbort(false);
      setLocal([]);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.sessions });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.overview });
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.history(sessionId) });
    },
  });
  const messages = [...local, ...(history.data ?? [])].filter(
    (m, i, a) => a.findIndex((x) => x.id === m.id) === i,
  );
  const mode = productModePresentation(overview.data?.mode);
  return (
    <DrawerShell
      title="Charlie"
      subtitle={
        <span className="flex flex-wrap items-center gap-2">
          <span>AI assistance within your authorized Astronomer scope</span>
          <span className="rounded-full border px-2 py-0.5 font-medium" aria-label={`Current Charlie mode: ${mode.label}`}>
            {mode.label}
          </span>
          <span className="basis-full text-xs text-muted-foreground">{mode.ceiling}</span>
        </span>
      }
      onClose={() => setOpen(false)}
      actions={
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => {
              setRestoreAttempted(true);
              setSessionId(undefined);
              setLocal([]);
            }}
            className="rounded-md border px-2 py-1 text-xs"
          >
            New chat
          </button>
          {sessionId && (
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
      bodyClassName="flex flex-col gap-4"
    >
      <div
        id="charlie-assistant-drawer"
        className="flex flex-wrap gap-2"
        role="list"
        aria-label="Attached context"
      >
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
        <div role="listitem">
          <ContextPicker />
        </div>
      </div>
      <p className="text-xs text-muted-foreground">
        Only the identifiers shown above are attached. Logs, metrics, audit
        details, and broad resource data are never attached automatically.
      </p>
      <div
        className="min-h-[16rem] flex-1 space-y-3"
        role="log"
        aria-live="polite"
        aria-relevant="additions text"
        aria-busy={send.isPending}
        aria-label="Charlie conversation"
      >
        {messages.length === 0 ? (
          <EmptyState
            icon={Bot}
            title="Ask Charlie"
            description="Investigate, explain, or plan work using the selected context."
          />
        ) : (
          messages.map((m) => (
            <article
              key={m.id}
              aria-label={m.role === "user" ? "Message from you" : "Message from Charlie"}
              className={cn(
                "rounded-lg border p-3",
                m.role === "user" ? "ml-8 bg-primary/5" : "mr-8 bg-card",
              )}
            >
              <p className="mb-1 text-xs font-medium text-muted-foreground">
                {m.role === "user" ? "You" : "Charlie"}
              </p>
              <SafeMarkdown streaming={m.state === "streaming"}>
                {m.content}
              </SafeMarkdown>
              <CharlieMessageParts
                message={m}
                onApprovalChanged={() =>
                  void qc.invalidateQueries({
                    queryKey: queryKeys.charlie.history(sessionId),
                  })
                }
              />
            </article>
          ))
        )}
      </div>
      {(send.isError || history.isError || streamUnavailable) && (
        <div
          role="alert"
          className="rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm"
        >
          <p>
            {streamUnavailable
              ? "The live Charlie stream is reconnecting. Confirmed history remains available, and your Astronomer data remains unchanged."
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
            {send.isError && send.variables && (
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
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const v = text.trim();
          if (v && !send.isPending) {
            setText("");
            send.mutate(v);
          }
        }}
        className="sticky bottom-0 space-y-2 bg-background pt-2"
      >
        <textarea
          aria-label="Message Charlie"
          value={text}
          onChange={(e) => setText(e.target.value)}
          rows={3}
          maxLength={sessionId ? 32768 : 4096}
          className="w-full resize-none rounded-lg border bg-background p-3 text-sm"
          placeholder="Ask Charlie…"
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
            disabled={!text.trim() || send.isPending}
            className="inline-flex items-center gap-2 rounded-md bg-primary px-3 py-2 text-sm text-primary-foreground transition-colors motion-reduce:transition-none disabled:opacity-50"
          >
            {send.isPending ? (
              <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
            ) : (
              <Send className="h-4 w-4" />
            )}
            Send
          </button>
        </div>
      </form>
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
