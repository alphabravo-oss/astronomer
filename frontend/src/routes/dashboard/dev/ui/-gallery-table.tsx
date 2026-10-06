import { useEffect, useState } from "react";
import { ActionButton } from "@/components/ui/action-button";
import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import { Row } from "./-gallery-shared";

interface DemoRow {
  name: string;
  status: string;
  version: string;
  nodes: number;
}

const ROWS: DemoRow[] = [
  { name: "prod-eu-1", status: "healthy", version: "v1.31.2", nodes: 12 },
  { name: "prod-us-1", status: "healthy", version: "v1.31.2", nodes: 9 },
  { name: "staging", status: "degraded", version: "v1.30.6", nodes: 4 },
  { name: "dev-sandbox", status: "pending", version: "v1.30.6", nodes: 2 },
  { name: "edge-ams", status: "failed", version: "v1.29.9", nodes: 3 },
];

const COLUMNS: Column<DemoRow>[] = [
  {
    key: "name",
    kind: "name",
    header: "Name",
    accessor: (r) => r.name,
    sortAccessor: (r) => r.name,
    sortable: true,
  },
  {
    key: "status",
    kind: "status",
    header: "Status",
    accessor: (r) => <StatusBadge status={r.status} />,
    sortAccessor: (r) => r.status,
    sortable: true,
  },
  {
    key: "version",
    kind: "version",
    header: "Version",
    accessor: (r) => r.version,
    sortAccessor: (r) => r.version,
  },
  {
    key: "nodes",
    kind: "count",
    header: "Nodes",
    accessor: (r) => r.nodes,
    sortAccessor: (r) => r.nodes,
    align: "right",
    sortable: true,
  },
];

export function TableDemo() {
  return (
    <DataTable
      data={ROWS}
      columns={COLUMNS}
      keyExtractor={(r) => r.name}
      searchable={false}
      pageSize={10}
    />
  );
}

export function DensityToggle() {
  const [density, setDensity] = useState<string>(
    () => document.documentElement.dataset.density ?? "comfortable",
  );
  useEffect(() => {
    const root = document.documentElement;
    const original = root.dataset.density;
    if (density === "compact") root.dataset.density = "compact";
    else delete root.dataset.density;
    return () => {
      if (original) root.dataset.density = original;
      else delete root.dataset.density;
    };
  }, [density]);
  return (
    <Row>
      <span className="text-sm text-muted-foreground">Density</span>
      {(["comfortable", "compact"] as const).map((d) => (
        <ActionButton
          key={d}
          size="sm"
          intent={density === d ? "primary" : "default"}
          aria-pressed={density === d}
          onClick={() => setDensity(d)}
        >
          {d}
        </ActionButton>
      ))}
    </Row>
  );
}
