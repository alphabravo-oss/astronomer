import { Loader2, ShieldAlert } from "lucide-react";
import type { ImageVulnProgress } from "@/lib/api/cluster-vulnerabilities";

// Sprint 081 — live scan-in-progress banner. Three render states map
// 1:1 to the operator's mental model of trivy: actively scanning,
// idle+up-to-date, operator-not-ready. Polling logic lives in the
// parent (3s while scanning, 30s idle); this component stays pure.
export function ScanProgressBanner({
  clusterId,
  progress,
  dispatchedRecently,
}: {
  clusterId: string;
  progress?: ImageVulnProgress;
  dispatchedRecently?: boolean;
}) {
  if (!progress) {
    return (
      <div className="rounded-lg border border-border bg-muted/30 px-4 py-2 text-sm text-muted-foreground">
        Loading scan state…
      </div>
    );
  }
  // "dispatched" state — operator clicked rescan in the last 30s but
  // the backend hasn't observed scanning jobs yet. Most likely trivy
  // hasn't created the Jobs yet (~1-3s lag) OR the scan already
  // finished between our polls. Either way, surface the click so the
  // user knows their action was received.
  if (dispatchedRecently && !progress.scanning) {
    return (
      <div className="rounded-lg border border-status-info/40 bg-status-info/5 px-4 py-3 text-sm flex items-center gap-3">
        <Loader2 className="h-4 w-4 animate-spin text-status-info shrink-0" />
        <span className="text-foreground">
          Rescan dispatched — waiting for trivy-operator to spawn new scan jobs…
        </span>
      </div>
    );
  }
  if (!progress.trivyOperatorReady) {
    // Trivy is opt-in: a cluster may simply not have it (the operator may run
    // a different scanner like NeuVector, or none). Make this a calm, clearly
    // actionable notice rather than an error — and point straight to Tools.
    return (
      <div className="rounded-lg border border-status-info/40 bg-status-info/5 px-4 py-3 text-sm flex items-start gap-3">
        <ShieldAlert className="h-4 w-4 text-status-info shrink-0 mt-0.5" />
        <div className="flex-1">
          <div className="font-medium text-foreground">
            Image scanning isn&apos;t enabled on this cluster
          </div>
          <div className="text-xs text-muted-foreground mt-0.5">
            Astronomer&apos;s built-in scanning uses the Trivy operator, which
            isn&apos;t installed here — so there are no vulnerability reports.
            If you already use a different scanner (e.g. NeuVector), you can
            ignore this. To turn on Astronomer scanning, install Trivy from the
            Tools tab; reports appear within the first scan window (~5–15 min).
          </div>
          <a
            href={`/dashboard/clusters/${clusterId}/tools`}
            className="inline-flex items-center gap-1.5 px-3 py-1.5 mt-2 rounded-md bg-primary text-primary-foreground text-xs font-medium hover:opacity-90"
          >
            Enable Trivy from Tools
          </a>
        </div>
      </div>
    );
  }
  if (progress.scanning) {
    const total =
      progress.activeJobs + progress.completedJobs + progress.failedJobs;
    const done = progress.completedJobs + progress.failedJobs;
    const pct = total > 0 ? Math.round((done / total) * 100) : 0;
    return (
      <div className="rounded-lg border border-status-info/40 bg-status-info/5 px-4 py-3 text-sm space-y-2">
        <div className="flex items-center justify-between gap-3">
          <div className="flex items-center gap-2 text-foreground">
            <Loader2 className="h-4 w-4 animate-spin text-status-info" />
            <span className="font-medium">
              Scanning {progress.activeJobs} workload
              {progress.activeJobs === 1 ? "" : "s"}…
            </span>
            <span className="text-xs text-muted-foreground tabular-nums">
              ({done}/{total} complete
              {progress.failedJobs > 0 ? `, ${progress.failedJobs} failed` : ""}
              )
            </span>
          </div>
          <span className="text-xs text-muted-foreground tabular-nums">
            {pct}%
          </span>
        </div>
        <div className="h-1.5 w-full rounded-full bg-status-info/15 overflow-hidden">
          <div
            className="h-full bg-status-info transition-all duration-500"
            style={{ width: total > 0 ? `${Math.max(5, pct)}%` : "50%" }}
          />
        </div>
      </div>
    );
  }
  const age = progress.lastScanAgeSeconds;
  let ageStr = "never";
  if (age != null) {
    if (age < 60) ageStr = `${age}s ago`;
    else if (age < 3600) ageStr = `${Math.round(age / 60)}m ago`;
    else if (age < 86400) ageStr = `${Math.round(age / 3600)}h ago`;
    else ageStr = `${Math.round(age / 86400)}d ago`;
  }
  return (
    <div className="rounded-lg border border-status-success/40 bg-status-success/5 px-4 py-2.5 text-sm flex items-center gap-2">
      <ShieldAlert className="h-4 w-4 text-status-success shrink-0" />
      <span className="text-foreground">
        All scans current — {progress.reportsCount} workload
        {progress.reportsCount === 1 ? "" : "s"} indexed, last scan {ageStr}.
      </span>
    </div>
  );
}
