import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
  getImageVulnReport,
  type CVERow,
  type CVESeverity,
} from "@/lib/api/cluster-vulnerabilities";
import { queryKeys } from "@/lib/query-keys";
import { pageCountLabel, pageTableCount } from "@/lib/api/pagination";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { PermissionState } from "@/components/ui/empty-state";
import { DataTable } from "@/components/ui/data-table";

interface Props {
  clusterId: string;
  reportId: string;
  severity: CVESeverity | "";
}

export function ReportCVEs(props: Props) {
  return (
    <PagedReportCVEs
      key={`${props.clusterId}/${props.reportId}/${props.severity}`}
      {...props}
    />
  );
}

function PagedReportCVEs({ clusterId, reportId, severity }: Props) {
  const [pageIndex, setPageIndex] = useState(0);
  const read = usePermissionDecision("clusters", "read", {
    type: "cluster",
    id: clusterId,
  });
  const query = useQuery({
    queryKey: queryKeys.clusterPages.imageVulnReport(
      clusterId,
      reportId,
      severity,
      pageIndex,
    ),
    queryFn: ({ signal }) =>
      getImageVulnReport(
        clusterId,
        reportId,
        { severity: severity || undefined, limit: 25, offset: pageIndex * 25 },
        signal,
      ),
    enabled: read.allowed,
    throwOnError: false,
  });
  const page =
    query.isError || !read.allowed ? undefined : query.data?.vulnerabilities;
  if (!read.allowed) return <PermissionState permission="clusters:read" />;
  return (
    <section className="space-y-3" aria-label="Report CVEs">
      <p className="text-xs text-muted-foreground">
        {pageCountLabel(page)} CVEs matching filter
      </p>
      <DataTable
        data={page?.data ?? []}
        columns={[
          {
            key: "cve",
            header: "CVE",
            sortable: false,
            accessor: (row) => <CVECard row={row} />,
          },
        ]}
        keyExtractor={(row) => row.id}
        searchable={false}
        loading={query.isLoading}
        isError={query.isError}
        error={query.error}
        permission="clusters:read"
        onRetry={() => void query.refetch()}
        emptyState={{
          title: "No CVEs on this page",
          description:
            "Change the severity filter or return to the previous page.",
        }}
        pageSize={25}
        serverSide={{
          ...pageTableCount(page),
          pagination: { pageIndex, pageSize: 25 },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
        }}
      />
    </section>
  );
}

function cveToneFor(severity: CVESeverity): string {
  switch (severity) {
    case "CRITICAL":
      return "bg-status-error/10 text-status-error border-status-error/30";
    case "HIGH":
      return "bg-status-high/10 text-status-high border-status-high/30";
    case "MEDIUM":
      return "bg-status-warning/10 text-status-warning border-status-warning/30";
    case "LOW":
      return "bg-status-info/10 text-status-info border-status-info/30";
    default:
      return "bg-muted text-muted-foreground border-border";
  }
}

function CVECard({ row }: { row: CVERow }) {
  return (
    <div className={`border rounded-md p-3 ${cveToneFor(row.severity)}`}>
      <div className="flex items-center justify-between gap-2">
        <a
          href={row.primaryLink || "#"}
          target="_blank"
          rel="noopener noreferrer"
          className="font-mono text-sm font-semibold underline"
        >
          {row.vulnerabilityId}
        </a>
        <span className="text-xs uppercase tracking-wide">
          {row.severity}
          {row.cvssScore != null && ` · CVSS ${row.cvssScore.toFixed(1)}`}
        </span>
      </div>
      {row.title && <div className="mt-1 text-sm">{row.title}</div>}
      <div className="mt-1 text-xs text-muted-foreground font-mono">
        {row.pkgName} {row.installedVersion} → fixed in{" "}
        {row.fixedVersion || "(no fix yet)"}
      </div>
    </div>
  );
}
