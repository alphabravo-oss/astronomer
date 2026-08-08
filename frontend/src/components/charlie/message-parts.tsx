import { useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  Clock,
  FileSearch,
  Loader2,
  ShieldCheck,
  Wrench,
} from "lucide-react";
import { SafeMarkdown, safeLink } from "./safe-markdown";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { can } from "@/lib/permissions";
import { useAuthStore } from "@/lib/store";
import {
  decideCharlieApproval,
  type CharlieApproval,
  type CharlieMessage,
  type CharlieToolRun,
} from "@/lib/api/charlie";

export function CharlieLifecycleNotice({ state }: { state?: string }) {
  const states: Record<
    string,
    { title: string; description: string; tone: string }
  > = {
    reconnecting: {
      title: "Reconnecting",
      description:
        "Restoring the event stream from the last confirmed revision.",
      tone: "text-status-warning",
    },
    retrying: {
      title: "Retrying",
      description:
        "Charlie is retrying a bounded operation. No additional authority is granted.",
      tone: "text-status-warning",
    },
    partial: {
      title: "Partial response",
      description:
        "Some requested evidence was unavailable; review citations and gaps.",
      tone: "text-status-warning",
    },
    central_unavailable: {
      title: "Charlie central unavailable",
      description:
        "Astronomer remains available. Charlie features are temporarily degraded.",
      tone: "text-status-error",
    },
    agent_failover: {
      title: "Agent failover",
      description:
        "The standby product agent is taking leadership; execution remains fenced.",
      tone: "text-status-warning",
    },
    policy_denied: {
      title: "Denied by product policy",
      description:
        "Astronomer rejected the requested capability before dispatch.",
      tone: "text-status-error",
    },
    expired: {
      title: "Session expired",
      description: "Start a new session to refresh authorization and context.",
      tone: "text-muted-foreground",
    },
    waiting_approval: {
      title: "Waiting for exact approval",
      description: "No action runs until an eligible operator confirms this exact bounded request.",
      tone: "text-status-warning",
    },
    mcp_denied: {
      title: "MCP request denied",
      description: "Astronomer rejected the requested tool at the private product boundary.",
      tone: "text-status-error",
    },
    disabled: {
      title: "Charlie disabled",
      description: "New Charlie sessions and work are disabled for this installation.",
      tone: "text-muted-foreground",
    },
    read_only_finding: {
      title: "Read-only finding",
      description: "Charlie produced a diagnosis and safe checks, but no write can run.",
      tone: "text-status-warning",
    },
    approval_required: {
      title: "Approval required",
      description: "Review the exact capability, effect, target, and permission before deciding.",
      tone: "text-status-warning",
    },
    auto_blocked: {
      title: "Automatic action blocked",
      description: "An Astronomer policy, scope, budget, or safety control prevented execution.",
      tone: "text-status-error",
    },
    destructive_denied: {
      title: "Destructive action denied",
      description: "Destructive and irreversible operations are unavailable in every Charlie mode.",
      tone: "text-status-error",
    },
    verification_failed: {
      title: "Verification failed",
      description: "The bounded action did not satisfy its postcondition. Further incident work is stopped.",
      tone: "text-status-error",
    },
    emergency_stopped: {
      title: "Emergency stop active",
      description: "Product authority is locally closed while central state is reconciled.",
      tone: "text-status-error",
    },
  };
  const current = state ? states[state] : undefined;
  if (!current) return null;
  return (
    <div
      role={
        state === "policy_denied" || state === "central_unavailable"
          ? "alert"
          : "status"
      }
      className="mt-2 flex gap-2 rounded-md border border-border p-2 text-xs"
    >
      <AlertTriangle className={`h-4 w-4 shrink-0 ${current.tone}`} />
      <span>
        <b className="block">{current.title}</b>
        {current.description}
      </span>
    </div>
  );
}

export function CharlieMessageParts({
  message,
  onApprovalChanged,
}: {
  message: CharlieMessage;
  onApprovalChanged?: () => void;
}) {
  return (
    <div className="space-y-2">
      <CharlieLifecycleNotice state={message.state} />
      {message.retrieval && (
        <div className="rounded-md border border-border p-2 text-xs">
          <div className="flex items-center gap-2">
            <FileSearch className="h-4 w-4" />
            <b>Retrieval</b>
            <StatusBadge status={message.retrieval.state} />
            {message.retrieval.documentCount != null && (
              <span>{message.retrieval.documentCount} documents</span>
            )}
          </div>
          {message.retrieval.summary && (
            <p className="mt-1 text-muted-foreground">
              {message.retrieval.summary.slice(0, 300)}
            </p>
          )}
        </div>
      )}
      {message.citations?.length ? (
        <div className="rounded-md border border-border p-2 text-xs">
          <b>Charlie-managed sources</b>
          <ul className="mt-1 space-y-1">
            {message.citations.slice(0, 20).map((c) => {
              const href = c.href ? safeLink(c.href) : null;
              return (
                <li key={c.id}>
                  {href ? (
                    <a
                      href={href}
                      target={href.startsWith("/") ? undefined : "_blank"}
                      rel="noopener noreferrer"
                      className="text-primary underline"
                    >
                      {c.title.slice(0, 160)}
                    </a>
                  ) : (
                    <span>{c.title.slice(0, 160)}</span>
                  )}{" "}
                  <span className="text-muted-foreground">
                    · {c.source.slice(0, 80)}
                  </span>
                </li>
              );
            })}
          </ul>
        </div>
      ) : null}
      {message.tools?.slice(0, 20).map((tool) => (
        <ToolCard key={tool.id} tool={tool} />
      ))}
      {message.approval && (
        <ApprovalCard
          approval={message.approval}
          onChanged={onApprovalChanged}
        />
      )}
    </div>
  );
}

function ToolCard({ tool }: { tool: CharlieToolRun }) {
  return (
    <details className="rounded-md border border-border p-2 text-xs">
      <summary className="flex cursor-pointer list-none items-center gap-2">
        <Wrench className="h-4 w-4" />
        <b className="flex-1">{tool.capability.slice(0, 128)}</b>
        <StatusBadge
          status={tool.state === "complete" ? "succeeded" : tool.state}
          label={tool.state}
        />
      </summary>
      <dl className="mt-2 grid grid-cols-2 gap-2">
        <div>
          <dt className="text-muted-foreground">Effect</dt>
          <dd>{tool.effect.slice(0, 240)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Risk</dt>
          <dd>{tool.risk.slice(0, 120)}</dd>
        </div>
      </dl>
      <p className="mt-2 text-muted-foreground">Argument fields</p>
      <p className="mt-1 rounded bg-muted p-2 text-[11px]">
        {tool.argumentSummary?.length
          ? tool.argumentSummary.slice(0, 20).join(", ")
          : "No display-safe argument fields were provided."}
      </p>
      {tool.result && (
        <div className="mt-2">
          <p className="text-muted-foreground">Result summary</p>
          <SafeMarkdown>{tool.result.slice(0, 500)}</SafeMarkdown>
        </div>
      )}
      {tool.auditCorrelationId && (
        <p className="mt-2 break-all text-muted-foreground">
          Audit correlation:{" "}
          <span className="font-mono">
            {tool.auditCorrelationId.slice(0, 128)}
          </span>
        </p>
      )}
    </details>
  );
}

function ApprovalCard({
  approval,
  onChanged,
}: {
  approval: CharlieApproval;
  onChanged?: () => void;
}) {
  const user = useAuthStore((s) => s.user);
  const permitted = can(user, "charlie", "approve");
  const [pending, setPending] = useState<"approve" | "deny">();
  const [confirm, setConfirm] = useState<"approve" | "deny">();
  const [rationale, setRationale] = useState("");
  const [error, setError] = useState("");
  const decide = async (decision: "approve" | "deny") => {
    setError("");
    setPending(decision);
    try {
      await decideCharlieApproval(approval.id, decision, rationale);
      setConfirm(undefined);
      onChanged?.();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Decision failed");
    } finally {
      setPending(undefined);
    }
  };
  return (
    <section
      aria-label="Charlie approval"
      className="rounded-md border border-status-warning/40 bg-status-warning/5 p-3 text-xs"
    >
      <div className="flex items-center gap-2">
        <ShieldCheck className="h-4 w-4" />
        <b className="flex-1">{approval.title.slice(0, 160)}</b>
        <StatusBadge status={approval.state} />
      </div>
      <dl className="mt-2 grid grid-cols-2 gap-2">
        <div>
          <dt className="text-muted-foreground">Capability</dt>
          <dd>{approval.capability.slice(0, 128)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Effect</dt>
          <dd>{approval.effect ?? "bounded write"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Required permission</dt>
          <dd>{approval.requiredPermission ?? "Exact target permission"}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Target</dt>
          <dd>{approval.target.slice(0, 200)}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">Risk</dt>
          <dd>{approval.risk.slice(0, 120)}</dd>
        </div>
        {approval.expiresAt && (
          <div>
            <dt className="text-muted-foreground">Expires</dt>
            <dd>{approval.expiresAt}</dd>
          </div>
        )}
      </dl>
      {approval.eligible && approval.state === "pending" && permitted ? (
        <div className="mt-3 space-y-2">
          <label className="block">
            <span className="text-muted-foreground">Rationale (optional, 512 characters)</span>
            <textarea
              aria-label={`Rationale for ${approval.title}`}
              value={rationale}
              maxLength={512}
              rows={2}
              onChange={(event) => setRationale(event.target.value)}
              className="mt-1 w-full rounded border bg-background p-2"
            />
          </label>
          <div className="flex gap-2">
            <button
              disabled={!!pending}
              onClick={() => setConfirm("approve")}
              className="rounded bg-primary px-3 py-2 text-primary-foreground"
            >
              {pending === "approve" ? (
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <CheckCircle2 className="h-4 w-4" />
              )}
              <span>Review approval</span>
            </button>
            <button
              disabled={!!pending}
              onClick={() => setConfirm("deny")}
              className="rounded border px-3 py-2"
            >
              {pending === "deny" ? (
                <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Clock className="h-4 w-4" />
              )}
              <span>Review denial</span>
            </button>
          </div>
        </div>
      ) : (
        <p className="mt-2 text-muted-foreground">
          {approval.reason ||
            (!approval.eligible
              ? "Charlie did not confirm server-side eligibility."
              : !permitted
                ? "Requires charlie:approve plus the underlying target permission."
                : "This approval is no longer pending.")}
        </p>
      )}
      {error && (
        <p role="alert" className="mt-2 text-status-error">
          {error}
        </p>
      )}
      <ConfirmDialog
        open={!!confirm}
        onClose={() => setConfirm(undefined)}
        onConfirm={() => confirm && void decide(confirm)}
        title={
          confirm === "approve"
            ? "Approve exact Charlie action"
            : "Deny exact Charlie action"
        }
        description={`${approval.capability} on ${approval.target}. This decision applies only to the displayed bounded action${rationale.trim() ? " and records your rationale" : ""}.`}
        confirmText={confirm === "approve" ? "Approve exact action" : "Deny exact action"}
        loading={!!pending}
        variant={confirm === "deny" ? "destructive" : undefined}
      />
    </section>
  );
}
