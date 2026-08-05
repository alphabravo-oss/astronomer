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
import { can } from "@/lib/permissions";
import { useAuthStore } from "@/lib/store";
import {
  decideCharlieApproval,
  type CharlieApproval,
  type CharlieMessage,
  type CharlieToolRun,
} from "@/lib/api/charlie";

const sensitiveKey =
  /(secret|token|password|private.?key|credential|certificate|authorization|cookie)/i;
export function boundedToolArguments(
  value: Record<string, unknown>,
): Record<string, unknown> {
  const visit = (input: unknown, depth: number): unknown => {
    if (depth > 2) return "[bounded]";
    if (typeof input === "string") return input.slice(0, 200);
    if (
      typeof input === "number" ||
      typeof input === "boolean" ||
      input == null
    )
      return input;
    if (Array.isArray(input))
      return input.slice(0, 10).map((v) => visit(v, depth + 1));
    if (typeof input === "object")
      return Object.fromEntries(
        Object.entries(input as Record<string, unknown>)
          .slice(0, 20)
          .map(([k, v]) => [
            k,
            sensitiveKey.test(k) ? "[redacted]" : visit(v, depth + 1),
          ]),
      );
    return String(input).slice(0, 200);
  };
  return visit(value, 0) as Record<string, unknown>;
}

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
        <StatusBadge status={tool.state} />
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
      <p className="mt-2 text-muted-foreground">Bounded arguments</p>
      <pre className="mt-1 max-h-36 overflow-auto rounded bg-muted p-2 text-[11px]">
        {JSON.stringify(boundedToolArguments(tool.arguments), null, 2)}
      </pre>
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
  const [error, setError] = useState("");
  const decide = async (decision: "approve" | "deny") => {
    setError("");
    setPending(decision);
    try {
      await decideCharlieApproval(approval.id, decision);
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
        <div className="mt-3 flex gap-2">
          <button
            disabled={!!pending}
            onClick={() => void decide("approve")}
            className="rounded bg-primary px-3 py-2 text-primary-foreground"
          >
            {pending === "approve" ? (
              <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
            ) : (
              <CheckCircle2 className="h-4 w-4" />
            )}
            <span className="sr-only">Approve exact Charlie action</span>
          </button>
          <button
            disabled={!!pending}
            onClick={() => void decide("deny")}
            className="rounded border px-3 py-2"
          >
            {pending === "deny" ? (
              <Loader2 className="h-4 w-4 animate-spin motion-reduce:animate-none" />
            ) : (
              <Clock className="h-4 w-4" />
            )}
            <span className="sr-only">Deny exact Charlie action</span>
          </button>
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
    </section>
  );
}
