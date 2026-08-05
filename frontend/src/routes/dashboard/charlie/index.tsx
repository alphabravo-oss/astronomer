import { createFileRoute } from "@tanstack/react-router";
import type { KeyboardEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  AlertTriangle,
  Bot,
  CheckCircle2,
  Clock,
  Loader2,
  ShieldCheck,
} from "lucide-react";
import { EmptyState, StatePanel } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { SafeMarkdown, safeLink } from "@/components/charlie/safe-markdown";
import { CharlieMessageParts } from "@/components/charlie/message-parts";
import { useRouter, useSearchParams } from "@/lib/navigation";
import { Link } from "@/lib/link";
import {
  decideCharlieApproval,
  getCharlieFinding,
  getCharlieHistory,
  listCharlieApprovals,
  listCharlieFindings,
  listCharlieSessions,
  transitionCharlieFinding,
} from "@/lib/api/charlie";
import { cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { useAuthStore } from "@/lib/store";
import { can } from "@/lib/permissions";
import {
  adjacentTab,
  mergeCharlieSearch,
} from "@/components/charlie/admin-utils";

export const CHARLIE_HUB_TABS = [
  "conversations",
  "investigations",
  "findings",
  "approvals",
] as const;
type Tab = (typeof CHARLIE_HUB_TABS)[number];
export function normalizeCharlieTab(value: string | null): Tab {
  return CHARLIE_HUB_TABS.includes(value as Tab)
    ? (value as Tab)
    : "conversations";
}
export const Route = createFileRoute("/dashboard/charlie/")({
  component: CharlieHub,
});

function CharlieHub() {
  const params = useSearchParams();
  const router = useRouter();
  const tab = normalizeCharlieTab(params.get("tab"));
  const set = (updates: Record<string, string | undefined>) => {
    router.push(`/dashboard/charlie?${mergeCharlieSearch(params, updates)}`);
  };
  const onTabKey = (event: KeyboardEvent<HTMLButtonElement>) => {
    const next = adjacentTab(CHARLIE_HUB_TABS, tab, event.key);
    if (!next) return;
    event.preventDefault();
    set({ tab: next });
    document.getElementById(`charlie-hub-tab-${next}`)?.focus();
  };
  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold">Charlie</h1>
        <p className="text-sm text-muted-foreground">
          Conversations, investigations, findings, and explicitly authorized
          actions.
        </p>
      </div>
      <div
        role="tablist"
        aria-label="Charlie sections"
        className="flex gap-1 overflow-x-auto border-b"
      >
        {CHARLIE_HUB_TABS.map((t) => (
          <button
            key={t}
            id={`charlie-hub-tab-${t}`}
            type="button"
            role="tab"
            aria-selected={tab === t}
            aria-controls={`charlie-hub-panel-${t}`}
            tabIndex={tab === t ? 0 : -1}
            onKeyDown={onTabKey}
            onClick={() => set({ tab: t })}
            className={cn(
              "min-h-11 border-b-2 px-4 py-2 text-sm capitalize transition-colors motion-reduce:transition-none",
              tab === t
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground",
            )}
          >
            {t}
          </button>
        ))}
      </div>
      <div
        id={`charlie-hub-panel-${tab}`}
        role="tabpanel"
        tabIndex={0}
        aria-labelledby={`charlie-hub-tab-${tab}`}
      >
        {tab === "conversations" && (
          <Conversations
            selected={params.get("session")}
            onSelect={(id) => set({ session: id })}
          />
        )}
        {tab === "investigations" && (
          <Investigations
            selected={params.get("incident")}
            onSelect={(id) => set({ incident: id })}
          />
        )}
        {tab === "findings" && (
          <Findings
            selected={params.get("finding")}
            onSelect={(id) => set({ finding: id })}
          />
        )}
        {tab === "approvals" && (
          <Approvals selected={params.get("approval")} />
        )}
      </div>
    </div>
  );
}

function QueryFailure({ label, retry }: { label: string; retry?: () => void }) {
  return (
    <EmptyState
      icon={AlertTriangle}
      title={`${label} unavailable`}
      description="This Charlie gateway capability is unavailable or you do not have permission. No action was taken."
      actionLabel={retry ? "Retry" : undefined}
      onAction={retry}
    />
  );
}
function Conversations({
  selected,
  onSelect,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const q = useQuery({
    queryKey: queryKeys.charlie.sessions,
    queryFn: listCharlieSessions,
    retry: false,
  });
  const h = useQuery({
    queryKey: queryKeys.charlie.history(selected),
    queryFn: () => getCharlieHistory(selected!),
    enabled: !!selected,
    retry: false,
  });
  if (q.isLoading)
    return <Loader2 aria-label="Loading conversations" className="h-5 w-5 animate-spin motion-reduce:animate-none" />;
  if (q.isError)
    return (
      <QueryFailure label="Conversations" retry={() => void q.refetch()} />
    );
  return (
    <div className="grid gap-4 md:grid-cols-[18rem_1fr]">
      <div className="space-y-2">
        {q.data?.map((s) => (
          <button
            key={s.id}
            onClick={() => onSelect(s.id)}
            className={cn(
              "w-full rounded-lg border p-3 text-left",
              selected === s.id && "border-primary",
            )}
          >
            <b className="block truncate text-sm">{s.intent}</b>
            <StatusBadge status={s.state} />
          </button>
        ))}
      </div>
      <div className="rounded-lg border p-4">
        {!selected ? (
          <EmptyState
            icon={Bot}
            title="Select a conversation"
            description="Conversation IDs stay in the URL for refresh and navigation."
          />
        ) : h.isError ? (
          <QueryFailure label="Conversation" retry={() => void h.refetch()} />
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
function Investigations({
  selected,
  onSelect,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const q = useQuery({
    queryKey: queryKeys.charlie.sessions,
    queryFn: listCharlieSessions,
    retry: false,
  });
  if (q.isError)
    return (
      <QueryFailure label="Investigations" retry={() => void q.refetch()} />
    );
  const rows = q.data?.filter((s) => s.visibility === "incident") ?? [];
  return rows.length ? (
    <div className="space-y-2">
      {rows.map((s) => (
        <button
          key={s.id}
          onClick={() => onSelect(s.id)}
          className={cn(
            "w-full rounded-lg border p-3 text-left",
            selected === s.id && "border-primary",
          )}
        >
          <b>{s.intent}</b>
          <span className="ml-2 text-xs text-muted-foreground">
            {s.resourceScopeSummary}
          </span>
        </button>
      ))}
    </div>
  ) : (
    <EmptyState
      icon={Clock}
      title="No investigations"
      description="Incident-scoped Charlie sessions appear here."
    />
  );
}
function Findings({
  selected,
  onSelect,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
}) {
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const canTriage = can(user, "charlie", "read");
  const q = useQuery({
    queryKey: queryKeys.charlie.findings,
    queryFn: listCharlieFindings,
    retry: false,
  });
  const d = useQuery({
    queryKey: queryKeys.charlie.finding(selected),
    queryFn: () => getCharlieFinding(selected!),
    enabled: !!selected,
    retry: false,
  });
  const action = useMutation({
    mutationFn: ({
      id,
      a,
    }: {
      id: string;
      a: "acknowledge" | "dismiss" | "resolve";
    }) => transitionCharlieFinding(id, a),
    onSuccess: () =>
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.findings }),
  });
  if (q.isError) return <QueryFailure label="Findings" />;
  return (
    <div className="grid gap-4 md:grid-cols-[20rem_1fr]">
      <div className="space-y-2">
        {q.data?.map((f) => (
          <button
            key={f.id}
            onClick={() => onSelect(f.id)}
            className={cn(
              "w-full rounded-lg border p-3 text-left",
              selected === f.id && "border-primary",
            )}
          >
            <div className="flex justify-between">
              <b className="text-sm">{f.title}</b>
              <StatusBadge status={f.severity} />
            </div>
            <p className="text-xs text-muted-foreground">
              {f.affectedResource.type}: {f.affectedResource.id}
            </p>
          </button>
        ))}
      </div>
      <div className="rounded-lg border p-4">
        {!selected ? (
          <EmptyState
            icon={ShieldCheck}
            title="Select a finding"
            description="Evidence is fetched from Charlie only when selected."
          />
        ) : d.isLoading ? (
          <StatePanel
            icon={Loader2}
            iconClassName="animate-spin motion-reduce:animate-none"
            title="Loading authorized finding detail"
          />
        ) : d.isError ? (
          <QueryFailure label="Finding" retry={() => void d.refetch()} />
        ) : (
          d.data && (
            <div className="space-y-4">
              <h2 className="text-lg font-semibold">{d.data.title}</h2>
              <SafeMarkdown>{d.data.summary}</SafeMarkdown>
              <p className="text-sm">
                Confidence:{" "}
                {d.data.confidence == null
                  ? "Not provided"
                  : `${Math.round(d.data.confidence * 100)}%`}
              </p>
              {d.data.reasonNoAction && (
                <p className="rounded bg-muted p-3 text-sm">
                  No action: {d.data.reasonNoAction}
                </p>
              )}
              {d.data.evidence?.length ? (
                <section className="space-y-2">
                  <h3 className="text-sm font-medium">Bounded evidence</h3>
                  {d.data.evidence.slice(0, 20).map((e, i) => {
                    const href = e.citation?.href
                      ? safeLink(e.citation.href)
                      : null;
                    return (
                      <div
                        key={`${e.label}:${i}`}
                        className="rounded border p-3 text-sm"
                      >
                        <b>{e.label.slice(0, 160)}</b>
                        <p className="mt-1 text-muted-foreground">
                          {e.summary.slice(0, 500)}
                        </p>
                        {href && (
                          <a
                            href={href}
                            target={href.startsWith("/") ? undefined : "_blank"}
                            rel="noopener noreferrer"
                            className="mt-1 inline-block text-xs text-primary underline"
                          >
                            {e.citation?.title}
                          </a>
                        )}
                      </div>
                    );
                  })}
                </section>
              ) : null}
              {d.data.operatorChecks?.length ? (
                <section>
                  <h3 className="text-sm font-medium">Operator checks</h3>
                  <ul className="mt-2 list-disc space-y-1 pl-5 text-sm">
                    {d.data.operatorChecks.slice(0, 20).map((check, i) => (
                      <li key={i}>{check.slice(0, 500)}</li>
                    ))}
                  </ul>
                </section>
              ) : null}
              {d.data.proposedAction && (
                <div className="space-y-2 rounded border p-3 text-sm">
                  <b>{d.data.proposedAction.capability}</b>
                  <p>Target: {d.data.proposedAction.target}</p>
                  <p>
                    Risk: {d.data.proposedAction.risk} · mode{" "}
                    {d.data.proposedAction.mode}
                  </p>
                  <p>{d.data.proposedAction.impact}</p>
                  {d.data.proposedAction.preconditions.length > 0 && (
                    <div>
                      <b className="text-xs">Preconditions</b>
                      <ul className="list-disc pl-5">
                        {d.data.proposedAction.preconditions
                          .slice(0, 20)
                          .map((v, i) => (
                            <li key={i}>{v.slice(0, 300)}</li>
                          ))}
                      </ul>
                    </div>
                  )}
                  {d.data.proposedAction.expectedResult && (
                    <p>
                      <b>Expected result:</b>{" "}
                      {d.data.proposedAction.expectedResult.slice(0, 500)}
                    </p>
                  )}
                  {d.data.proposedAction.rollback && (
                    <p>
                      <b>Rollback:</b>{" "}
                      {d.data.proposedAction.rollback.slice(0, 500)}
                    </p>
                  )}
                  {d.data.proposedAction.verification && (
                    <p>
                      <b>Verification:</b>{" "}
                      {d.data.proposedAction.verification.slice(0, 500)}
                    </p>
                  )}
                  <p>
                    Approval eligibility:{" "}
                    {d.data.proposedAction.eligible
                      ? "Confirmed by Charlie"
                      : "Not eligible"}
                  </p>
                  {d.data.proposedAction.approvalId && (
                    <p>
                      Linked approval:{" "}
                      <span className="font-mono">
                        {d.data.proposedAction.approvalId}
                      </span>{" "}
                      ·{" "}
                      {d.data.proposedAction.eligible
                        ? "pending and unexpired"
                        : "not currently eligible"}
                    </p>
                  )}
                  {d.data.proposedAction.eligible &&
                    d.data.proposedAction.approvalId && (
                      <Link
                        href={`/dashboard/charlie?tab=approvals&approval=${encodeURIComponent(d.data.proposedAction.approvalId)}`}
                        className="inline-flex rounded-md border px-3 py-2 text-sm text-primary"
                      >
                        Open exact approval flow
                      </Link>
                    )}
                </div>
              )}
              {canTriage ? (
                <div className="flex flex-wrap gap-2">
                  {(["acknowledge", "dismiss", "resolve"] as const).map((a) => (
                    <button
                      key={a}
                      disabled={action.isPending}
                      onClick={() => action.mutate({ id: d.data.id, a })}
                      className="rounded-md border px-3 py-2 text-sm capitalize"
                    >
                      {a}
                    </button>
                  ))}
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Requires charlie:read to update finding lifecycle.
                </p>
              )}
              {action.isError && (
                <p role="alert" className="text-sm text-status-error">
                  The finding was not changed. Access may have changed or
                  Charlie is unavailable.
                </p>
              )}
            </div>
          )
        )}
      </div>
    </div>
  );
}
function Approvals({ selected }: { selected: string | null }) {
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const canApprove = can(user, "charlie", "approve");
  const q = useQuery({
    queryKey: queryKeys.charlie.approvals,
    queryFn: listCharlieApprovals,
    retry: false,
  });
  const decide = useMutation({
    mutationFn: ({ id, d }: { id: string; d: "approve" | "deny" }) =>
      decideCharlieApproval(id, d),
    onSuccess: () =>
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.approvals }),
  });
  if (q.isError)
    return <QueryFailure label="Approvals" retry={() => void q.refetch()} />;
  return (
    <div className="space-y-3">
      {q.data?.map((a) => (
        <article
          key={a.id}
          id={`approval-${a.id}`}
          className={cn(
            "rounded-lg border p-4",
            selected === a.id && "border-primary ring-1 ring-primary",
          )}
        >
          <div className="flex justify-between">
            <h2 className="font-medium">{a.title}</h2>
            <StatusBadge status={a.state} />
          </div>
          <p className="mt-2 text-sm">
            {a.capability} on {a.target} · risk {a.risk}
          </p>
          {a.eligible && a.state === "pending" && canApprove ? (
            <div className="mt-3 flex gap-2">
              <button
                onClick={() => decide.mutate({ id: a.id, d: "approve" })}
                className="rounded bg-primary px-3 py-2 text-sm text-primary-foreground"
              >
                Approve
              </button>
              <button
                onClick={() => decide.mutate({ id: a.id, d: "deny" })}
                className="rounded border px-3 py-2 text-sm"
              >
                Deny
              </button>
            </div>
          ) : (
            <p className="mt-2 text-xs text-muted-foreground">
              {!a.eligible
                ? "Charlie did not confirm server-side eligibility."
                : !canApprove
                  ? "Requires charlie:approve plus the underlying target permission."
                  : "This approval is no longer pending."}
            </p>
          )}
        </article>
      ))}
      {q.data?.length === 0 && (
        <EmptyState
          icon={CheckCircle2}
          title="No pending approvals"
          description="Only server-confirmed eligible approvals can be acted on here."
        />
      )}
      {decide.isError && (
        <p role="alert" className="text-sm text-status-error">
          The decision was not accepted. No action was executed.
        </p>
      )}
    </div>
  );
}
