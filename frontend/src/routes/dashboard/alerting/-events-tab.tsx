import { Check, CheckCircle } from "lucide-react";
import { AlertInvestigation } from "./-alert-investigation";
import { useInvestigationParam } from "@/components/resources/resource-navigation-context";
import { useTabParam } from "@/lib/use-tab-param";
import {
  useAcknowledgeAlert,
  useAlertEventSummary,
  useAlertEvents,
  useResolveAlert,
} from "@/lib/hooks/alerting";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { ActionButton } from "@/components/ui/action-button";
import { cn, formatRelativeTime, statusBgColor } from "@/lib/utils";
import type { AlertEvent } from "@/types";
import { Select } from "@/components/ui/select";
import { pageTableCount } from "@/lib/api/pagination";

const ALERT_EVENTS_PAGE_SIZE = 50;

export function EventsTab({
  clusterId,
  history = true,
}: { clusterId?: string; history?: boolean } = {}) {
  const [pageValue, setPageValue] = useInvestigationParam("alertPage", "0");
  const pageIndex = /^\d+$/.test(pageValue)
    ? Math.min(Number(pageValue), 100000)
    : 0;
  const setPageIndex = (page: number) => setPageValue(String(page));
  const [selected, setSelected] = useInvestigationParam("event");
  const [status, setStatus] = useTabParam(
    ["", "firing", "acknowledged", "resolved", "silenced"] as const,
    "",
    "alertStatus",
  );
  const [severity, setSeverity] = useTabParam(
    ["", "critical", "warning", "info"] as const,
    "",
    "alertSeverity",
  );
  const {
    data: eventsPage,
    isLoading,
    isError,
    refetch,
  } = useAlertEvents({
    clusterId,
    status: history ? status || undefined : "firing",
    severity: severity || undefined,
    limit: ALERT_EVENTS_PAGE_SIZE,
    offset: pageIndex * ALERT_EVENTS_PAGE_SIZE,
  });
  const { data: summary } = useAlertEventSummary(clusterId);
  const acknowledgeAlert = useAcknowledgeAlert();
  const resolveAlert = useResolveAlert();
  const events = eventsPage?.data ?? [];

  const columns: Column<AlertEvent>[] = [
    {
      key: "severity",
      header: "Severity",
      accessor: (row) => (
        <span
          className={cn(
            "text-xs px-2 py-0.5 rounded-sm capitalize font-medium",
            statusBgColor(row.severity),
          )}
        >
          {row.severity}
        </span>
      ),
    },
    {
      key: "rule",
      header: "Rule",
      accessor: (row) => (
        <ActionButton
          intent="ghost"
          size="sm"
          onClick={() => setSelected(row.id)}
        >
          {row.ruleName || "Inspect alert"}
        </ActionButton>
      ),
    },
    {
      key: "message",
      header: "Message",
      accessor: (row) => (
        <span className="text-sm text-muted-foreground truncate max-w-[300px] block">
          {row.message}
        </span>
      ),
      sortable: false,
    },
    ...(clusterId
      ? []
      : [
          {
            key: "cluster",
            header: "Cluster",
            accessor: (row: AlertEvent) => (
              <span className="text-sm text-muted-foreground">
                {row.clusterName || "--"}
              </span>
            ),
          } as Column<AlertEvent>,
        ]),
    {
      key: "firedAt",
      header: "Fired",
      accessor: (row) => (
        <span className="text-xs text-muted-foreground">
          {formatRelativeTime(row.firedAt)}
        </span>
      ),
    },
    {
      key: "status",
      header: "Status",
      accessor: (row) => <StatusBadge status={row.status} />,
    },
    {
      key: "actions",
      header: "",
      accessor: (row) => (
        <div className="flex items-center gap-1">
          {row.status === "firing" && (
            <>
              <ActionButton
                size="sm"
                intent="ghost"
                title="Acknowledge"
                disabled={acknowledgeAlert.isPending || resolveAlert.isPending}
                onClick={() => acknowledgeAlert.mutate(row.id)}
                icon={<Check className="h-3 w-3" />}
                className="h-auto px-2 py-1"
              >
                Ack
              </ActionButton>
              <ActionButton
                size="sm"
                intent="ghost"
                title="Resolve"
                disabled={acknowledgeAlert.isPending || resolveAlert.isPending}
                onClick={() => resolveAlert.mutate(row.id)}
                icon={<CheckCircle className="h-3 w-3" />}
                className="h-auto px-2 py-1 hover:text-status-success hover:bg-status-success/10"
              >
                Resolve
              </ActionButton>
            </>
          )}
          {row.status === "acknowledged" && (
            <ActionButton
              size="sm"
              intent="ghost"
              title="Resolve"
              disabled={acknowledgeAlert.isPending || resolveAlert.isPending}
              onClick={() => resolveAlert.mutate(row.id)}
              icon={<CheckCircle className="h-3 w-3" />}
              className="h-auto px-2 py-1 hover:text-status-success hover:bg-status-success/10"
            >
              Resolve
            </ActionButton>
          )}
        </div>
      ),
      sortable: false,
    },
  ];

  const serverColumns = columns.map((column) => ({
    ...column,
    sortable: false,
  }));

  return (
    <div className="space-y-3">
      {selected && (
        <AlertInvestigation id={selected} onClose={() => setSelected("")} />
      )}
      {(acknowledgeAlert.isError || resolveAlert.isError) && (
        <p role="alert" className="text-sm text-destructive">
          The alert update failed. Retry after reviewing the error.
        </p>
      )}
      <div
        className="grid grid-cols-2 gap-2 sm:grid-cols-4"
        aria-label="Alert event totals"
      >
        {[
          ["Firing", summary?.firing],
          ["Acknowledged", summary?.acknowledged],
          ["Resolved", summary?.resolved],
          ["Silenced", summary?.silenced],
        ].map(([label, value]) => (
          <div
            key={String(label)}
            className="rounded-md border border-border bg-card px-3 py-2"
          >
            <div className="text-xs text-muted-foreground">{label}</div>
            <div className="text-lg font-semibold tabular-nums">
              {value ?? "—"}
            </div>
          </div>
        ))}
      </div>
      <DataTable
        data={events}
        columns={serverColumns}
        keyExtractor={(row) => row.id}
        searchable={false}
        pageSize={ALERT_EVENTS_PAGE_SIZE}
        loading={isLoading}
        isError={isError}
        onRetry={() => refetch()}
        filtersActive={Boolean(status || severity)}
        onClearFilters={() => {
          setStatus("");
          setSeverity("");
          setPageIndex(0);
        }}
        serverSide={{
          ...pageTableCount(eventsPage),
          pagination: { pageIndex, pageSize: ALERT_EVENTS_PAGE_SIZE },
          onPaginationChange: (next) => setPageIndex(next.pageIndex),
        }}
        toolbar={
          <div className="flex items-center gap-2">
            {history && (
              <Select
                aria-label="Filter alert events by status"
                value={status}
                onChange={(event) => {
                  setStatus(event.target.value as "" | AlertEvent["status"]);
                  setPageIndex(0);
                }}
                containerClassName="w-auto"
              >
                <option value="">All statuses</option>
                <option value="firing">Firing</option>
                <option value="acknowledged">Acknowledged</option>
                <option value="resolved">Resolved</option>
                <option value="silenced">Silenced</option>
              </Select>
            )}
            <Select
              aria-label="Filter alert events by severity"
              value={severity}
              onChange={(event) => {
                setSeverity(event.target.value as "" | AlertEvent["severity"]);
                setPageIndex(0);
              }}
              containerClassName="w-auto"
            >
              <option value="">All severities</option>
              <option value="critical">Critical</option>
              <option value="warning">Warning</option>
              <option value="info">Info</option>
            </Select>
          </div>
        }
        emptyState={{
          title: "No alert events",
          description:
            "There are no events matching the selected scope and filters.",
        }}
      />
    </div>
  );
}
