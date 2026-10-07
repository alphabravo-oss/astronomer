import { LoadingPanel } from "@/components/charlie/loading-panel";
import { Textarea } from "@/components/ui/textarea";
import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2 } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { StatusBadge } from "@/components/ui/status-badge";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import {
  decideCharlieApproval,
  listCharlieApprovals,
  type CharlieApproval,
} from "@/lib/api/charlie";
import { cn } from "@/lib/utils";
import { queryKeys } from "@/lib/query-keys";
import { useAuthStore } from "@/lib/store";
import { can } from "@/lib/permissions";
import { ActionButton } from "@/components/ui/action-button";
import { QueryFailure } from "./-shared";

export function Approvals({ selected }: { selected: string | null }) {
  const qc = useQueryClient();
  const user = useAuthStore((state) => state.user);
  const canApprove = can(user, "charlie", "approve");
  const [rationales, setRationales] = useState<Record<string, string>>({});
  const [confirm, setConfirm] = useState<{
    approval: CharlieApproval;
    decision: "approve" | "deny";
  }>();
  const q = useQuery({
    queryKey: queryKeys.charlie.approvals,
    queryFn: listCharlieApprovals,
    retry: false,
  });
  const decide = useMutation({
    mutationFn: ({
      id,
      d,
      rationale,
    }: {
      id: string;
      d: "approve" | "deny";
      rationale: string;
    }) => decideCharlieApproval(id, d, rationale),
    onSuccess: () => {
      setConfirm(undefined);
      void qc.invalidateQueries({ queryKey: queryKeys.charlie.approvals });
    },
  });
  if (q.isError) return <QueryFailure label="Approvals" query={q} />;
  if (q.isLoading) return <LoadingPanel title="Loading approvals" />;
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
          {a.review && (
            <section
              className="mt-3 rounded-md border bg-muted/30 p-3 text-sm"
              aria-label={`Review summary for ${a.title}`}
            >
              {a.review.description && <p>{a.review.description}</p>}
              <dl className="mt-2 grid gap-2 text-xs sm:grid-cols-2">
                {a.review.expectedImpact && (
                  <div>
                    <dt className="text-foreground/70">Expected impact</dt>
                    <dd>{a.review.expectedImpact}</dd>
                  </div>
                )}
                {a.review.reversible !== undefined && (
                  <div>
                    <dt className="text-foreground/70">Reversible</dt>
                    <dd>{a.review.reversible ? "Yes" : "No"}</dd>
                  </div>
                )}
                {a.review.rollback && (
                  <div>
                    <dt className="text-foreground/70">Rollback</dt>
                    <dd>{a.review.rollback}</dd>
                  </div>
                )}
                {a.review.destructive !== undefined && (
                  <div>
                    <dt className="text-foreground/70">Destructive</dt>
                    <dd>{a.review.destructive ? "Yes" : "No"}</dd>
                  </div>
                )}
                <div>
                  <dt className="text-foreground/70">Arguments</dt>
                  <dd>
                    {a.review.argumentsWithheld
                      ? "Withheld by Charlie"
                      : "Unavailable"}
                  </dd>
                </div>
              </dl>
            </section>
          )}
          <dl className="mt-2 grid gap-2 text-xs sm:grid-cols-2">
            <div>
              <dt className="text-foreground/70">Effect</dt>
              <dd>{a.effect ?? "bounded write"}</dd>
            </div>
            <div>
              <dt className="text-foreground/70">Required permission</dt>
              <dd>{a.requiredPermission ?? "Exact target permission"}</dd>
            </div>
            <div>
              <dt className="text-foreground/70">Expiry</dt>
              <dd>
                {a.expiresAt
                  ? new Date(a.expiresAt).toLocaleString()
                  : "Not provided"}
              </dd>
            </div>
            <div>
              <dt className="text-foreground/70">Eligibility</dt>
              <dd>{a.eligible ? "Confirmed" : (a.reason ?? "Not eligible")}</dd>
            </div>
          </dl>
          {a.eligible && a.state === "pending" && canApprove ? (
            <div className="mt-3 space-y-2">
              <label className="block text-xs">
                <span className="text-foreground/70">Rationale (optional)</span>
                <Textarea
                  aria-label={`Rationale for ${a.title}`}
                  maxLength={512}
                  rows={2}
                  value={rationales[a.id] ?? ""}
                  onChange={(event) =>
                    setRationales((current) => ({
                      ...current,
                      [a.id]: event.target.value,
                    }))
                  }
                  className="mt-1 w-full rounded-sm border bg-background p-2"
                />
              </label>
              <div className="flex gap-2">
                <ActionButton
                  intent="bare"
                  size="none"
                  onClick={() =>
                    setConfirm({ approval: a, decision: "approve" })
                  }
                  className="rounded-sm bg-primary px-3 py-2 text-sm text-primary-foreground"
                >
                  Review approval
                </ActionButton>
                <ActionButton
                  intent="bare"
                  size="none"
                  onClick={() => setConfirm({ approval: a, decision: "deny" })}
                  className="rounded-sm border px-3 py-2 text-sm"
                >
                  Review denial
                </ActionButton>
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
          terminal // queued by Charlie itself, not created here
        />
      )}
      {decide.isError && (
        <p role="alert" className="text-sm text-status-error">
          {decide.error instanceof Error
            ? decide.error.message
            : "The decision was not accepted. No action was executed."}
        </p>
      )}
      <ConfirmDialog
        open={!!confirm}
        onClose={() => setConfirm(undefined)}
        onConfirm={() =>
          confirm &&
          decide.mutate({
            id: confirm.approval.id,
            d: confirm.decision,
            rationale: rationales[confirm.approval.id] ?? "",
          })
        }
        title={
          confirm?.decision === "approve"
            ? "Approve exact Charlie action"
            : "Deny exact Charlie action"
        }
        description={
          confirm
            ? `${confirm.approval.review?.description ?? confirm.approval.capability} on ${confirm.approval.target}. ${confirm.approval.review?.destructive ? "Charlie marks this action as destructive. " : ""}No broader action is authorized.`
            : ""
        }
        confirmText={
          confirm?.decision === "approve"
            ? "Approve exact action"
            : "Deny exact action"
        }
        variant={
          confirm?.decision === "deny" || confirm?.approval.review?.destructive
            ? "destructive"
            : undefined
        }
        loading={decide.isPending}
      />
    </div>
  );
}
