import { CheckCircle2, XCircle } from "lucide-react";
import type { K8sCreateBatchResult } from "@/lib/hooks/kubernetes-proxy";
import { extractApiErrorMessage } from "@/lib/api/errors";
export function ResourceApplyResults({
  results,
}: {
  results: K8sCreateBatchResult[];
}) {
  return (
    <>
      {" "}
      {results.length > 0 && (
        <div
          className="max-h-40 overflow-y-auto border-b border-border bg-muted/20"
          aria-label="Resource creation results"
        >
          {results.map((result) => (
            <div
              key={`${result.path}/${result.label}`}
              className="flex items-start gap-2 border-b border-border/60 px-5 py-2 text-xs last:border-b-0"
            >
              {result.ok ? (
                <CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-success" />
              ) : (
                <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-status-error" />
              )}
              <span className="min-w-0 flex-1">
                <span className="font-mono text-foreground">
                  {result.label}
                </span>
                {!result.ok && (
                  <span className="ml-2 text-status-error">
                    {extractApiErrorMessage(result.error) ?? "Create failed"}
                  </span>
                )}
              </span>
              <span
                className={
                  result.ok ? "text-status-success" : "text-status-error"
                }
              >
                {result.ok ? "Created" : "Failed"}
              </span>
            </div>
          ))}
        </div>
      )}
    </>
  );
}
