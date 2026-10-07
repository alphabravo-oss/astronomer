import type { UseMutationResult } from "@tanstack/react-query";
import { SafeMarkdown, safeLink } from "@/components/charlie/safe-markdown";
import type { CharlieFinding } from "@/lib/api/charlie";
import {
  findingLifecycleDecisions,
  findingDecisionLabel,
  findingWorkflowLabel,
  findingWorkflowGuidance,
} from "@/components/charlie/finding-workflow";
import { ActionButton } from "@/components/ui/action-button";

export type FindingTransition = UseMutationResult<
  unknown,
  Error,
  { id: string; a: Parameters<typeof findingDecisionLabel>[0] }
>;

export function FindingDetail({
  finding,
  canTriage,
  action,
}: {
  finding: CharlieFinding;
  canTriage: boolean;
  action: FindingTransition;
}) {
  return (
    <div className="space-y-4">
      <h2 className="text-lg font-semibold">{finding.title}</h2>
      <p className="text-sm text-foreground/70">
        Workflow: {findingWorkflowLabel(finding)}
      </p>
      <p className="rounded-sm bg-muted p-3 text-sm">
        {findingWorkflowGuidance(finding)}
      </p>
      <SafeMarkdown>{finding.summary}</SafeMarkdown>
      <p className="text-sm">
        Confidence:{" "}
        {finding.confidence == null
          ? "Not provided"
          : `${Math.round(finding.confidence * 100)}%`}
      </p>
      {finding.reasonNoAction && (
        <p className="rounded-sm bg-muted p-3 text-sm">
          No action: {finding.reasonNoAction}
        </p>
      )}
      {finding.evidence?.length ? (
        <section className="space-y-2">
          <h3 className="text-sm font-medium">Bounded evidence</h3>
          {finding.evidence.slice(0, 20).map((e, i) => {
            const href = e.citation?.href ? safeLink(e.citation.href) : null;
            return (
              <div
                key={`${e.label}:${i}`}
                className="rounded-sm border p-3 text-sm"
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
      {finding.operatorChecks?.length ? (
        <section>
          <h3 className="text-sm font-medium">Operator checks</h3>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-sm">
            {finding.operatorChecks.slice(0, 20).map((check, i) => (
              <li key={i}>{check.slice(0, 500)}</li>
            ))}
          </ul>
        </section>
      ) : null}
      {finding.manualRemediation ? (
        <section className="space-y-2 rounded-sm border p-3 text-sm">
          <h3 className="font-medium">Manual remediation</h3>
          {finding.manualRemediation.preconditions.length ? (
            <div>
              <b className="text-xs">Preconditions</b>
              <ul className="list-disc pl-5">
                {finding.manualRemediation.preconditions
                  .slice(0, 16)
                  .map((value, index) => (
                    <li key={index}>{value.slice(0, 256)}</li>
                  ))}
              </ul>
            </div>
          ) : null}
          <div>
            <b className="text-xs">Steps</b>
            <ol className="list-decimal pl-5">
              {finding.manualRemediation.steps
                .slice(0, 16)
                .map((value, index) => (
                  <li key={index}>{value.slice(0, 512)}</li>
                ))}
            </ol>
          </div>
          <p>
            <b>Expected impact:</b>{" "}
            {finding.manualRemediation.expectedImpact.slice(0, 1024)}
          </p>
          {finding.manualRemediation.rollback ? (
            <p>
              <b>Rollback:</b>{" "}
              {finding.manualRemediation.rollback.slice(0, 1024)}
            </p>
          ) : null}
          <div>
            <b className="text-xs">
              Verification (
              {finding.manualRemediation.verificationMethod.slice(0, 128)})
            </b>
            <ul className="list-disc pl-5">
              {finding.manualRemediation.verificationSteps
                .slice(0, 16)
                .map((value, index) => (
                  <li key={index}>{value.slice(0, 512)}</li>
                ))}
            </ul>
          </div>
          <p className="text-foreground/70">
            Recording progress or completion does not authorize Charlie to
            execute.
          </p>
        </section>
      ) : null}
      {canTriage && findingLifecycleDecisions(finding).length ? (
        <div className="flex flex-wrap gap-2">
          {findingLifecycleDecisions(finding).map((a) => (
            <ActionButton
              intent="bare"
              size="none"
              key={a}
              disabled={action.isPending}
              onClick={() => action.mutate({ id: finding.id, a })}
              className="rounded-md border px-3 py-2 text-sm capitalize"
            >
              {findingDecisionLabel(a)}
            </ActionButton>
          ))}
        </div>
      ) : !canTriage ? (
        <p className="text-sm text-foreground/70">
          Requires charlie:update to update finding lifecycle.
        </p>
      ) : null}
      {action.isError && (
        <p role="alert" className="text-sm text-status-error">
          The finding was not changed. Access may have changed or Charlie is
          unavailable.
        </p>
      )}
    </div>
  );
}
