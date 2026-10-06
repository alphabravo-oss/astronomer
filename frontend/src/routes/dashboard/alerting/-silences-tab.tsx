import { useAlertSilences } from "@/lib/hooks/alerting";
import { CappedChips, TimestampCell } from "@/components/ui/cell-primitives";
import { DataTable, type Column } from "@/components/ui/data-table";
import type { AlertSilence } from "@/types";

export function SilencesTab() {
  const { data: silences, isLoading, isError, refetch } = useAlertSilences();

  const columns: Column<AlertSilence>[] = [
    {
      key: "reason",
      header: "Reason",
      kind: "text",
      grow: true,
      minSize: 200,
      maxSize: 560,
      accessor: (row) => (
        <span className="font-medium text-foreground">{row.reason}</span>
      ),
    },
    {
      key: "duration",
      header: "Duration",
      kind: "text",
      size: 96,
      minSize: 88,
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.duration}</span>
      ),
    },
    {
      key: "matchers",
      header: "Matchers",
      kind: "badge",
      size: 260,
      minSize: 220,
      maxSize: 340,
      accessor: (row) => (
        <CappedChips
          items={Object.entries(row.matchers).map(([k, v]) => `${k}=${v}`)}
        />
      ),
      sortable: false,
    },
    {
      key: "creator",
      header: "Creator",
      kind: "text",
      size: 200,
      minSize: 160,
      accessor: (row) => (
        <span className="text-sm text-muted-foreground">{row.createdBy}</span>
      ),
    },
    {
      key: "endsAt",
      header: "Expires",
      kind: "age",
      accessor: (row) => <TimestampCell value={row.endsAt} />,
    },
  ];

  return (
    <DataTable
      data={silences || []}
      columns={columns}
      keyExtractor={(row) => row.id}
      searchPlaceholder="Search silences..."
      loading={isLoading}
      isError={isError}
      onRetry={() => refetch()}
      emptyState={{
        title: "No active silences",
        description:
          "There are no active items requiring attention in this scope.",
      }}
    />
  );
}
