import { AlertTriangle, GitCompare } from "lucide-react";

import { apiErrorCode, apiErrorStatus } from "@/lib/api/errors";
import { cn } from "@/lib/utils";

export type ResourceApplyFailure =
  | "conflict"
  | "forbidden"
  | "retryable"
  | "terminal";

/** Classify recovery from structured transport metadata, never message text. */
export function classifyResourceApplyFailure(
  error: unknown,
): ResourceApplyFailure {
  const status = apiErrorStatus(error);
  const code = apiErrorCode(error);
  if (status === 409 || code === "conflict" || code === "ownership_conflict") {
    return "conflict";
  }
  if (status === 401 || status === 403) return "forbidden";
  if (
    status === undefined ||
    status === 408 ||
    status === 425 ||
    status === 429 ||
    status >= 500
  ) {
    return "retryable";
  }
  return "terminal";
}

export interface YamlApplyPreviewModel {
  previewFor: string;
  changed: boolean;
  diff: YamlDiff;
  warnings: string[];
}

interface YamlDiff {
  added: number;
  removed: number;
  lines: Array<{
    type: "context" | "add" | "remove";
    text: string;
    key: string;
  }>;
}

function normalizeText(value: string): string {
  return value.replace(/\r\n/g, "\n").trimEnd();
}

export function yamlTextMatches(left: string, right: string): boolean {
  return normalizeText(left) === normalizeText(right);
}

export function buildYamlDiff(before: string, after: string): YamlDiff {
  const beforeLines = normalizeText(before).split("\n");
  const afterLines = normalizeText(after).split("\n");
  let start = 0;
  while (
    start < beforeLines.length &&
    start < afterLines.length &&
    beforeLines[start] === afterLines[start]
  ) {
    start++;
  }
  let endBefore = beforeLines.length - 1;
  let endAfter = afterLines.length - 1;
  while (
    endBefore >= start &&
    endAfter >= start &&
    beforeLines[endBefore] === afterLines[endAfter]
  ) {
    endBefore--;
    endAfter--;
  }
  const contextStart = Math.max(0, start - 3);
  const contextEndBefore = Math.min(beforeLines.length - 1, endBefore + 3);
  const contextEndAfter = Math.min(afterLines.length - 1, endAfter + 3);
  const lines: YamlDiff["lines"] = [];
  for (let i = contextStart; i < start; i++) {
    lines.push({
      type: "context",
      text: beforeLines[i] ?? "",
      key: `c-pre-${i}`,
    });
  }
  for (let i = start; i <= endBefore; i++) {
    lines.push({ type: "remove", text: beforeLines[i] ?? "", key: `r-${i}` });
  }
  for (let i = start; i <= endAfter; i++) {
    lines.push({ type: "add", text: afterLines[i] ?? "", key: `a-${i}` });
  }
  const contextAfterStart = Math.max(start, endBefore + 1);
  const contextAfterEnd = Math.max(contextEndBefore, contextEndAfter);
  for (
    let i = contextAfterStart;
    i <= contextAfterEnd && i < beforeLines.length;
    i++
  ) {
    if (i < start || i <= endBefore) continue;
    lines.push({
      type: "context",
      text: beforeLines[i] ?? "",
      key: `c-post-${i}`,
    });
  }
  return {
    added: Math.max(0, endAfter - start + 1),
    removed: Math.max(0, endBefore - start + 1),
    lines:
      lines.length > 0
        ? lines.slice(0, 240)
        : [
            {
              type: "context",
              text: "No changes after server-side normalization.",
              key: "none",
            },
          ],
  };
}

export function YamlDiffPreview({
  preview,
}: {
  preview: YamlApplyPreviewModel;
}) {
  return (
    <section
      aria-label="YAML apply preview"
      aria-live="polite"
      className="max-h-[34%] border-t border-border bg-background"
    >
      <div className="flex items-center justify-between gap-3 border-b border-border px-3 py-2">
        <div className="flex items-center gap-2 text-sm font-medium text-foreground">
          <GitCompare className="h-4 w-4" />
          Apply preview
        </div>
        <div className="text-xs tabular-nums text-muted-foreground">
          +{preview.diff.added} / -{preview.diff.removed}
        </div>
      </div>
      {preview.warnings.length > 0 && (
        <div className="space-y-1 border-b border-status-warning/20 bg-status-warning/10 px-3 py-2 text-xs text-status-warning">
          {preview.warnings.map((warning) => (
            <div key={warning} className="flex items-start gap-2">
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{warning}</span>
            </div>
          ))}
        </div>
      )}
      <pre className="max-h-48 overflow-auto px-3 py-2 text-xs leading-5">
        {preview.diff.lines.map((line) => (
          <div
            key={line.key}
            className={cn(
              "min-w-max font-mono",
              line.type === "add" && "bg-status-success/10 text-status-success",
              line.type === "remove" && "bg-status-error/10 text-status-error",
              line.type === "context" && "text-muted-foreground",
            )}
          >
            <span className="inline-block w-4 select-none">
              {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
            </span>
            {line.text}
          </div>
        ))}
      </pre>
    </section>
  );
}
