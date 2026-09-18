// §Schema Tier-1 — table renderer. First-party, TEXT-ONLY: every cell value is
// run through a closed-enum formatter and placed in a text node, never
// dangerouslySetInnerHTML. Runs no third-party JS — it paints the proxied rows.

import { DataTable, type Column } from "@/components/ui/data-table";
import { StatusBadge } from "@/components/ui/status-badge";
import {
  getByPath,
  formatValue,
  tableColumns,
  type ProxyRow,
} from "./declarative";
import type { FieldBinding } from "@/lib/api/extensions";

export interface ExtTableProps {
  rows: ProxyRow[];
  fields?: FieldBinding[];
  emptyText?: string;
}

function Cell({ row, field }: { row: ProxyRow; field: FieldBinding }) {
  const raw = getByPath(row, field.path);
  const text = formatValue(raw, field.format);
  if (field.format === "badge") {
    return <StatusBadge status={text} size="sm" />;
  }
  return <span className="text-foreground">{text}</span>;
}

export function ExtTable({ rows, fields, emptyText }: ExtTableProps) {
  const columns = tableColumns(rows, fields);

  if (rows.length === 0 || columns.length === 0) {
    return (
      <p className="px-3 py-6 text-center text-sm text-muted-foreground">
        {emptyText || "No data"}
      </p>
    );
  }

  const tableDefs: Column<ProxyRow>[] = columns.map((field) => ({
    key: field.path,
    header: field.label,
    accessor: (row) => <Cell row={row} field={field} />,
    sortAccessor: (row) =>
      formatValue(getByPath(row, field.path), field.format),
  }));

  return (
    <DataTable
      data={rows}
      columns={tableDefs}
      keyExtractor={(row) => String(row.id ?? row.name ?? rows.indexOf(row))}
      searchable={false}
    />
  );
}
