import { useQuery } from "@tanstack/react-query";
import { getImageDatabaseStatus } from "@/lib/api/cluster-vulnerabilities";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";

export function ImageDatabaseStatus({ clusterId, reportId }: {clusterId: string; reportId: string}) {
 const read = usePermissionDecision("clusters", "read", {type: "cluster", id: clusterId});
 const query = useQuery({
  queryKey: ["image-database-status-v1", clusterId, reportId],
  queryFn: ({signal}) => getImageDatabaseStatus(clusterId, reportId, signal),
  enabled: read.allowed,
  refetchInterval: 60_000,
  throwOnError: false,
 });
 if (!read.allowed) return <PermissionState permission="clusters:read" />;
 const status = query.isError ? undefined : query.data;
 // Unsupported read categories and unmeasured current workload never become clean.
 const qualifiedClean = status?.eligible_for_clean_posture === true && status.current_workload_checked && status.finding_count === 0 && status.scan_state === "complete" && status.main_state === "fresh" && !status.report_expired && !status.rescan_required && ((status.java_applicability === "needed" && status.java_state === "fresh") || (status.java_applicability === "not_needed" && status.java_state === "not_needed"));
 return (
  <section className="border border-border rounded-md p-3 space-y-2" aria-label="Database freshness" aria-live="polite">
   <h3 className="text-xs font-medium uppercase">Database freshness</h3>
   {!status ? (
    <p className="text-xs text-muted-foreground">{query.isLoading ? "Checking database provenance…" : "Database provenance unavailable. Findings remain visible below."}</p>
   ) : (
    <>
     <p className="text-sm">Scan: {status.scan_state}. Main database: {status.main_state}. Java database: {status.java_state}.</p>
     <p className="text-xs text-muted-foreground">
      {status.finding_count > 0 ? `${status.finding_count} findings remain reported.` : qualifiedClean ? "No findings in the qualified current scan." : "No findings reported; current clean posture is unqualified."}
      {status.report_expired ? " Report has expired." : ""}
      {status.rescan_required ? " Rescan required." : ""}
      {status.java_applicability === "unknown" ? " Java applicability is unknown." : ""}
     </p>
     {!status.current_workload_checked && <p className="text-xs text-muted-foreground">Current workload identity has not been checked by this read. Scan completion does not prove database freshness.</p>}
     {status.resource_capacity_exhausted && <p role="alert" className="text-xs text-status-warning">Scan resource capacity is exhausted. Inspect unresolved terminal resource UID ownership before releasing capacity.</p>}
     <p className="text-xs text-muted-foreground">Observation revision {status.observation_revision}; witness revision {status.witness_revision}; cycle {status.cycle}; accepted database generation {status.accepted_generation}.</p>
    </>
   )}
  </section>
 );
}
