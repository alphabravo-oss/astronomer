import { useEffect, useState } from "react";
import { Check, Copy, Loader2 } from "lucide-react";

import type { CharlieTurnProgress } from "./turn-progress";

export function CharlieProgressIndicator({
  progress,
}: {
  progress: CharlieTurnProgress;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const elapsedSeconds = Math.max(
    0,
    Math.floor((now - progress.startedAt) / 1000),
  );
  const toolCalls = progress.toolCallIds.length;
  const completedTools = progress.completedToolCallIds.length;
  const failedTools = progress.failedToolCallIds.length;
  const blockedTools = progress.blockedToolCallIds.length;
  const pendingTools = Math.max(0, toolCalls - completedTools);
  const quietSeconds = Math.max(
    0,
    Math.floor((now - progress.lastEventAt) / 1000),
  );
  const delayed = quietSeconds >= 30;
  const stalled = quietSeconds >= 90;
  let activity = progress.label;
  if (delayed && progress.stage === "analyzing") {
    activity =
      completedTools > 0
        ? `Waiting for Charlie's model to analyze ${completedTools} tool ${completedTools === 1 ? "result" : "results"}`
        : "Waiting for Charlie's model to analyze the available evidence";
  } else if (
    delayed &&
    (progress.stage === "queued" || progress.stage === "planning")
  ) {
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
        <Loader2 className="mt-0.5 h-4 w-4 shrink-0 animate-spin text-primary motion-reduce:animate-none" />
        <div className="min-w-0 flex-1">
          <p className="text-xs font-medium text-muted-foreground">
            Charlie is working
          </p>
          <p className="truncate text-sm" title={activity}>
            {activity}
          </p>
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
          style={{
            animation: "charlie-progress-slide 1.4s ease-in-out infinite",
          }}
        />
      </div>
      <div className="mt-2 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-foreground">
        <span>{elapsedSeconds}s elapsed</span>
        {toolCalls > 0 && (
          <span>
            {completedTools} of {toolCalls} tool{" "}
            {toolCalls === 1 ? "call" : "calls"} finished
          </span>
        )}
        {pendingTools > 0 && <span>{pendingTools} active or pending</span>}
        {failedTools > 0 && <span>{failedTools} failed</span>}
        {blockedTools > 0 && <span>{blockedTools} safely blocked</span>}
        {progress.eventCount > 0 && (
          <span>
            {progress.eventCount.toLocaleString()} live{" "}
            {progress.eventCount === 1 ? "update" : "updates"}
          </span>
        )}
        {delayed && <span>Last update {quietSeconds}s ago</span>}
      </div>
      {stalled && (
        <p className="mt-2 text-xs text-status-warning">
          This step is taking longer than expected. Charlie will stop it at the
          configured deadline; you can keep waiting or stop the request.
        </p>
      )}
    </article>
  );
}

export function CopyMessageButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  if (!text.trim()) return null;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      const area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.left = "-9999px";
      document.body.appendChild(area);
      area.select();
      document.execCommand("copy");
      document.body.removeChild(area);
    }
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };
  return (
    <button
      type="button"
      className="inline-flex items-center gap-1 rounded-sm border px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
      aria-label={copied ? "Copied" : "Copy message"}
      onClick={() => void copy()}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      {copied ? "Copied" : "Copy"}
    </button>
  );
}
