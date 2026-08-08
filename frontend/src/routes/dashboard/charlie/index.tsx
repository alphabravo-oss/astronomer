import { createFileRoute } from "@tanstack/react-router";
import { useState, type KeyboardEvent } from "react";
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
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { SafeMarkdown, safeLink } from "@/components/charlie/safe-markdown";
import { CharlieMessageParts } from "@/components/charlie/message-parts";
import { useRouter, useSearchParams } from "@/lib/navigation";
import { Link } from "@/lib/link";
import {
  decideCharlieApproval,
  getCharlieFinding,
  getCharlieHistory,
  getCharlieOverview,
  listCharlieApprovals,
  listCharlieFindings,
  listCharlieSessions,
  listCharlieThreads,
  getCharlieThreadHistory,
  transitionCharlieFinding,
  type CharlieApproval,
} from "@/lib/api/charlie";
import { productModePresentation } from "@/components/charlie/charlie-shell";
import {
  findingLifecycleDecisions,
  findingDecisionLabel,
  findingWorkflowLabel,
  findingWorkflowGuidance,
} from "@/components/charlie/finding-workflow";
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
  const overview = useQuery({
    queryKey: queryKeys.charlie.overview,
    queryFn: getCharlieOverview,
  });
  const mode = productModePresentation(overview.data?.mode);
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
      <div className="space-y-2">
        <div className="flex flex-wrap items-center gap-3">
          <h1 className="text-2xl font-semibold">Charlie</h1>
          <span
            className={cn(
              "rounded-full border px-2.5 py-0.5 text-xs font-semibold",
              mode.badgeClass,
            )}
            aria-label={`Current Charlie mode: ${mode.label}`}
            data-testid="charlie-hub-mode-badge"
            data-mode={mode.key}
          >
            Mode: {mode.label}
          </span>
        </div>
        <p className="text-sm text-foreground/70">
          Conversations, investigations, findings, and explicitly authorized
          actions.
        </p>
        <p className="text-xs text-muted-foreground">{mode.ceiling}</p>
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
                : "border-transparent text-foreground/70",
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
            params={params}
            set={set}
          />
        )}
        {tab === "findings" && (
          <Findings
            selected={params.get("finding")}
            onSelect={(id) => set({ finding: id })}
            params={params}
            set={set}
          />
        )}
        {tab === "approvals" && (
          <Approvals selected={params.get("approval")} />
        )}
      </div>
    </div>
  );
}

function FilterField({
  label,
  value,
  onChange,
  options,
  type = "text",
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options?: string[];
  type?: "text" | "date";
}) {
  return (
    <label className="min-w-36 space-y-1 text-xs">
      <span className="text-foreground/70">{label}</span>
      {options ? (
        <select aria-label={label} value={value} onChange={(event) => onChange(event.target.value)} className="h-9 w-full rounded border bg-background px-2">
          <option value="">All</option>
          {options.map((option) => <option key={option} value={option}>{option.replaceAll("_", " ")}</option>)}
        </select>
      ) : (
        <input type={type} aria-label={label} value={value} onChange={(event) => onChange(event.target.value)} className="h-9 w-full rounded border bg-background px-2" />
      )}
    </label>
  );
}

function resourceHref(type: string, id: string): string {
  switch (type) {
    case "installation": return `/dashboard/clusters/${encodeURIComponent(id)}`;
    case "agent_connection_record": return `/dashboard/agents?connection=${encodeURIComponent(id)}`;
    case "alert": return `/dashboard/alerting?alert=${encodeURIComponent(id)}`;
    case "backup": return `/dashboard/backups?backup=${encodeURIComponent(id)}`;
    case "self_management_application": return "/dashboard/settings/gitops";
    default: return `/dashboard/search?q=${encodeURIComponent(id)}`;
  }
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
    return <Loader2 aria-label="Loading conversations" className="h-5 w-5 animate-spin motion-reduce:animate-none" />;
  if (q.isError)
    return (
      <QueryFailure label="Conversations" retry={() => void q.refetch()} />
    );
  return (
    <div className="grid gap-4 md:grid-cols-[18rem_1fr]">
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
            <b className="block truncate text-sm">{s.title || "Chat"}</b>
            <div className="mt-2 flex flex-wrap items-center gap-2">
              <StatusBadge status={s.state} />
              <StatusBadge status="private" label="Private chat" />
            </div>
          </button>
        ))}
        {rows.length === 0 && (
          <EmptyState
            icon={Bot}
            title="No private conversations"
            description="Your private Charlie chats appear here. Shared incident investigations are kept in the Investigations tab."
          />
        )}
      </div>
      <div className="rounded-lg border p-4">
        {!selectedConversation ? (
          <EmptyState
            icon={Bot}
            title="Select a conversation"
            description="Only your private user-started conversations can be opened here."
          />
        ) : h.isLoading ? (
          <StatePanel
            icon={Loader2}
            iconClassName="animate-spin motion-reduce:animate-none"
            title="Loading private conversation"
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
  params,
  set,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
  params: URLSearchParams;
  set: (updates: Record<string, string | undefined>) => void;
}) {
  const q = useQuery({
    queryKey: queryKeys.charlie.sessions,
    queryFn: listCharlieSessions,
    retry: false,
  });
  const findings = useQuery({
    queryKey: queryKeys.charlie.findings,
    queryFn: listCharlieFindings,
    retry: false,
  });
  if (q.isError || findings.isError)
    return (
      <QueryFailure
        label="Investigations"
        retry={() => {
          void q.refetch();
          void findings.refetch();
        }}
      />
    );
  const status = params.get("status") ?? "";
  const severity = params.get("severity") ?? "";
  const cluster = params.get("cluster") ?? "";
  const source = params.get("source") ?? "";
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  // Investigations track: system/event sessions only — never private user chats.
  const rows = (q.data?.filter((s) => s.visibility === "incident" && s.source === "event") ?? [])
    .map((session) => ({ session, finding: findings.data?.find((finding) => finding.sessionId === session.id) }))
    .filter(({ session, finding }) =>
      (!status || session.state === status) &&
      (!severity || finding?.severity === severity) &&
      (!cluster || (
        finding?.affectedResource.type === "agent_connection_record" &&
        finding.affectedResource.id.toLowerCase().includes(cluster.toLowerCase())
      )) &&
      (!source || session.source === source || finding?.source === source) &&
      (!from || !session.createdAt || session.createdAt >= from) &&
      (!to || !session.createdAt || session.createdAt.slice(0, 10) <= to));
  const detail = rows.find(({ session }) => session.id === selected);
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-3 rounded-lg border p-3" aria-label="Investigation filters">
        <FilterField label="Investigation status" value={status} options={["active", "waiting_approval", "completed", "failed", "aborted"]} onChange={(value) => set({ status: value || undefined })} />
        <FilterField label="Investigation severity" value={severity} options={["medium", "warning", "high", "critical"]} onChange={(value) => set({ severity: value || undefined })} />
        <FilterField label="Agent connection record" value={cluster} onChange={(value) => set({ cluster: value || undefined })} />
        <FilterField label="Investigation source" value={source} options={["event", "user"]} onChange={(value) => set({ source: value || undefined })} />
        <FilterField type="date" label="From date" value={from} onChange={(value) => set({ from: value || undefined })} />
        <FilterField type="date" label="To date" value={to} onChange={(value) => set({ to: value || undefined })} />
        <p className="w-full text-xs text-foreground/70">
          Agent connection record matches only that exact resource type; other
          affected resources are not treated as clusters.
        </p>
      </div>
      {q.isLoading || findings.isLoading ? (
        <StatePanel
          icon={Loader2}
          iconClassName="animate-spin motion-reduce:animate-none"
          title="Loading authorized investigations"
        />
      ) : rows.length ? rows.map(({ session: s, finding }) => (
        <button
          key={s.id}
          onClick={() => onSelect(s.id)}
          className={cn(
            "w-full rounded-lg border p-3 text-left",
            selected === s.id && "border-primary",
          )}
        >
          <div className="flex flex-wrap items-center gap-2">
            <b>{s.intent}</b>
            <StatusBadge status="incident" label="Shared incident" />
            {finding && <StatusBadge status={finding.severity} />}
          </div>
          <span className="mt-1 block text-xs text-foreground/70">
            {s.resourceScopeSummary}
          </span>
        </button>
      )) : (
        <EmptyState
          icon={Clock}
          title="No investigations match"
          description="Shared incident investigations appear only while you can read every affected Astronomer resource. Try changing the filters."
        />
      )}
      {detail && (
        <section className="rounded-lg border p-4" aria-label="Investigation detail">
          <h2 className="font-medium">{detail.session.intent}</h2>
          <p className="mt-1 text-xs text-foreground/70">
            Shared incident metadata is visible because you can currently read
            every affected Astronomer resource. Evidence remains in Charlie and
            is fetched only through authorized detail requests.
          </p>
          {detail.finding && (
            <>
              <Link className="mt-2 inline-block text-sm text-primary underline" href={resourceHref(detail.finding.affectedResource.type, detail.finding.affectedResource.id)}>
                Open originating {detail.finding.affectedResource.type.replaceAll("_", " ")}
              </Link>
              <p className="mt-3 text-sm">Repeated {detail.finding.repeatCount ?? 1} time{(detail.finding.repeatCount ?? 1) === 1 ? "" : "s"}.</p>
              <ol className="mt-2 border-l pl-4 text-xs text-foreground/70" aria-label="Investigation timeline">
                {detail.finding.createdAt && <li>First observed {new Date(detail.finding.createdAt).toLocaleString()}</li>}
                {detail.finding.updatedAt && <li>Last observed {new Date(detail.finding.updatedAt).toLocaleString()}</li>}
              </ol>
            </>
          )}
        </section>
      )}
    </div>
  );
}
function Findings({
  selected,
  onSelect,
  params,
  set,
}: {
  selected: string | null;
  onSelect: (id: string) => void;
  params: URLSearchParams;
  set: (updates: Record<string, string | undefined>) => void;
}) {
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const canTriage = can(user, "charlie", "update");
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
      a: "acknowledge" | "start_remediation" | "request_verification" | "dismiss" | "resolve";
    }) => transitionCharlieFinding(id, a),
    onSuccess: (_data, variables) => {
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.findings });
      void qc.invalidateQueries({
        queryKey: queryKeys.charlie.finding(variables.id),
      });
    },
  });
  if (q.isError)
    return <QueryFailure label="Findings" retry={() => void q.refetch()} />;
  const status = params.get("status") ?? "";
  const severity = params.get("severity") ?? "";
  const source = params.get("source") ?? "";
  const resource = params.get("resource") ?? "";
  const block = params.get("block") ?? "";
  const from = params.get("from") ?? "";
  const to = params.get("to") ?? "";
  const rows = q.data?.filter((finding) =>
    (!status || finding.state === status) &&
    (!severity || finding.severity === severity) &&
    (!source || finding.source === source) &&
    (!resource || `${finding.affectedResource.type}:${finding.affectedResource.id}`.toLowerCase().includes(resource.toLowerCase())) &&
    (!block || finding.reasonNoAction === block) &&
    (!from || !finding.updatedAt || finding.updatedAt >= from) &&
    (!to || !finding.updatedAt || finding.updatedAt.slice(0, 10) <= to));
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap gap-3 rounded-lg border p-3" aria-label="Finding filters">
        <FilterField label="Finding status" value={status} options={["open", "acknowledged", "dismissed", "resolved", "expired"]} onChange={(value) => set({ status: value || undefined })} />
        <FilterField label="Finding severity" value={severity} options={["medium", "warning", "high", "critical"]} onChange={(value) => set({ severity: value || undefined })} />
        <FilterField label="Finding source" value={source} onChange={(value) => set({ source: value || undefined })} />
        <FilterField label="Affected resource" value={resource} onChange={(value) => set({ resource: value || undefined })} />
        <FilterField label="Execution block" value={block} onChange={(value) => set({ block: value || undefined })} />
        <FilterField type="date" label="Finding from date" value={from} onChange={(value) => set({ from: value || undefined })} />
        <FilterField type="date" label="Finding to date" value={to} onChange={(value) => set({ to: value || undefined })} />
      </div>
      <div className="grid gap-4 md:grid-cols-[20rem_1fr]">
      <div className="space-y-2">
        {q.isLoading ? (
          <StatePanel
            icon={Loader2}
            iconClassName="animate-spin motion-reduce:animate-none"
            title="Loading findings"
          />
        ) : rows?.length ? rows.map((f) => (
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
              <StatusBadge status={f.severity === "high" ? "error" : f.severity} label={f.severity} />
            </div>
            <p className="text-xs text-foreground/70">
              {f.affectedResource.type}: {f.affectedResource.id}
            </p>
            <p className="text-xs text-foreground/70">{f.repeatCount ?? 1} occurrence{(f.repeatCount ?? 1) === 1 ? "" : "s"}</p>
          </button>
        )) : (
          <EmptyState
            icon={ShieldCheck}
            title="No findings match"
            description="Try changing the finding filters."
          />
        )}
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
              <p className="text-sm text-foreground/70">
                Workflow: {findingWorkflowLabel(d.data)}
              </p>
              <p className="rounded bg-muted p-3 text-sm">
                {findingWorkflowGuidance(d.data)}
              </p>
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
                        <p className="mt-1 text-foreground/70">
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
              {d.data.manualRemediation ? (
                <section className="space-y-2 rounded border p-3 text-sm">
                  <h3 className="font-medium">Manual remediation</h3>
                  {d.data.manualRemediation.preconditions.length ? (
                    <div>
                      <b className="text-xs">Preconditions</b>
                      <ul className="list-disc pl-5">
                        {d.data.manualRemediation.preconditions.slice(0, 16).map((value, index) => (
                          <li key={index}>{value.slice(0, 256)}</li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  <div>
                    <b className="text-xs">Steps</b>
                    <ol className="list-decimal pl-5">
                      {d.data.manualRemediation.steps.slice(0, 16).map((value, index) => (
                        <li key={index}>{value.slice(0, 512)}</li>
                      ))}
                    </ol>
                  </div>
                  <p><b>Expected impact:</b> {d.data.manualRemediation.expectedImpact.slice(0, 1024)}</p>
                  {d.data.manualRemediation.rollback ? (
                    <p><b>Rollback:</b> {d.data.manualRemediation.rollback.slice(0, 1024)}</p>
                  ) : null}
                  <div>
                    <b className="text-xs">Verification ({d.data.manualRemediation.verificationMethod.slice(0, 128)})</b>
                    <ul className="list-disc pl-5">
                      {d.data.manualRemediation.verificationSteps.slice(0, 16).map((value, index) => (
                        <li key={index}>{value.slice(0, 512)}</li>
                      ))}
                    </ul>
                  </div>
                  <p className="text-foreground/70">Recording progress or completion does not authorize Charlie to execute.</p>
                </section>
              ) : null}
              {canTriage && findingLifecycleDecisions(d.data).length ? (
                <div className="flex flex-wrap gap-2">
                  {findingLifecycleDecisions(d.data).map((a) => (
                    <button
                      key={a}
                      disabled={action.isPending}
                      onClick={() => action.mutate({ id: d.data.id, a })}
                      className="rounded-md border px-3 py-2 text-sm capitalize"
                    >
                      {findingDecisionLabel(a)}
                    </button>
                  ))}
                </div>
              ) : !canTriage ? (
                <p className="text-sm text-foreground/70">
                  Requires charlie:update to update finding lifecycle.
                </p>
              ) : null}
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
    </div>
  );
}
function Approvals({ selected }: { selected: string | null }) {
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const canApprove = can(user, "charlie", "approve");
  const [rationales, setRationales] = useState<Record<string, string>>({});
  const [confirm, setConfirm] = useState<{ approval: CharlieApproval; decision: "approve" | "deny" }>();
  const q = useQuery({
    queryKey: queryKeys.charlie.approvals,
    queryFn: listCharlieApprovals,
    retry: false,
  });
  const decide = useMutation({
    mutationFn: ({ id, d, rationale }: { id: string; d: "approve" | "deny"; rationale: string }) =>
      decideCharlieApproval(id, d, rationale),
    onSuccess: () => {
      setConfirm(undefined);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.approvals });
    },
  });
  if (q.isError)
    return <QueryFailure label="Approvals" retry={() => void q.refetch()} />;
  if (q.isLoading)
    return (
      <StatePanel
        icon={Loader2}
        iconClassName="animate-spin motion-reduce:animate-none"
        title="Loading approvals"
      />
    );
  const approvals = [...(q.data ?? [])].sort((left, right) =>
    left.id === selected ? -1 : right.id === selected ? 1 : 0,
  );
  return (
    <div className="space-y-3">
      {approvals.map((a) => (
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
          <dl className="mt-2 grid gap-2 text-xs sm:grid-cols-2">
            <div><dt className="text-foreground/70">Effect</dt><dd>{a.effect ?? "bounded write"}</dd></div>
            <div><dt className="text-foreground/70">Required permission</dt><dd>{a.requiredPermission ?? "Exact target permission"}</dd></div>
            <div><dt className="text-foreground/70">Expiry</dt><dd>{a.expiresAt ? new Date(a.expiresAt).toLocaleString() : "Not provided"}</dd></div>
            <div><dt className="text-foreground/70">Eligibility</dt><dd>{a.eligible ? "Confirmed" : a.reason ?? "Not eligible"}</dd></div>
          </dl>
          {a.eligible && a.state === "pending" && canApprove ? (
            <div className="mt-3 space-y-2">
              <label className="block text-xs">
                <span className="text-foreground/70">Rationale (optional)</span>
                <textarea aria-label={`Rationale for ${a.title}`} maxLength={512} rows={2} value={rationales[a.id] ?? ""} onChange={(event) => setRationales((current) => ({ ...current, [a.id]: event.target.value }))} className="mt-1 w-full rounded border bg-background p-2" />
              </label>
              <div className="flex gap-2">
              <button
                onClick={() => setConfirm({ approval: a, decision: "approve" })}
                className="rounded bg-primary px-3 py-2 text-sm text-primary-foreground"
              >
                Review approval
              </button>
              <button
                onClick={() => setConfirm({ approval: a, decision: "deny" })}
                className="rounded border px-3 py-2 text-sm"
              >
                Review denial
              </button>
              </div>
            </div>
          ) : (
            <p className="mt-2 text-xs text-foreground/70">
              {!a.eligible
                ? "Charlie did not confirm server-side eligibility."
                : !canApprove
                  ? "Requires charlie:approve plus the underlying target permission."
                  : "This approval is no longer pending."}
            </p>
          )}
        </article>
      ))}
      {approvals.length === 0 && (
        <EmptyState
          icon={CheckCircle2}
          title="No pending approvals"
          description="Only server-confirmed eligible approvals can be acted on here."
        />
      )}
      {decide.isError && (
        <p role="alert" className="text-sm text-status-error">
          {decide.error instanceof Error ? decide.error.message : "The decision was not accepted. No action was executed."}
        </p>
      )}
      <ConfirmDialog
        open={!!confirm}
        onClose={() => setConfirm(undefined)}
        onConfirm={() => confirm && decide.mutate({ id: confirm.approval.id, d: confirm.decision, rationale: rationales[confirm.approval.id] ?? "" })}
        title={confirm?.decision === "approve" ? "Approve exact Charlie action" : "Deny exact Charlie action"}
        description={confirm ? `${confirm.approval.capability} on ${confirm.approval.target}. No broader action is authorized.` : ""}
        confirmText={confirm?.decision === "approve" ? "Approve exact action" : "Deny exact action"}
        variant={confirm?.decision === "deny" ? "destructive" : undefined}
        loading={decide.isPending}
      />
    </div>
  );
}
