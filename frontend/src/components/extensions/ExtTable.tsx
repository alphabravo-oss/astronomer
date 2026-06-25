'use client';

// §Schema Tier-1 — table renderer. First-party, TEXT-ONLY: every cell value is
// run through a closed-enum formatter and placed in a text node, never
// dangerouslySetInnerHTML. Runs no third-party JS — it paints the proxied rows.

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { StatusBadge } from '@/components/ui/status-badge';
import { getByPath, formatValue, tableColumns, type ProxyRow } from './declarative';
import type { FieldBinding } from '@/lib/api/extensions';

export interface ExtTableProps {
  rows: ProxyRow[];
  fields?: FieldBinding[];
  emptyText?: string;
}

function Cell({ row, field }: { row: ProxyRow; field: FieldBinding }) {
  const raw = getByPath(row, field.path);
  const text = formatValue(raw, field.format);
  if (field.format === 'badge') {
    return (
      <TableCell className="px-3 py-2">
        <StatusBadge status={text} size="sm" />
      </TableCell>
    );
  }
  return <TableCell className="px-3 py-2 text-foreground">{text}</TableCell>;
}

export function ExtTable({ rows, fields, emptyText }: ExtTableProps) {
  const columns = tableColumns(rows, fields);

  if (rows.length === 0 || columns.length === 0) {
    return (
      <p className="px-3 py-6 text-center text-sm text-muted-foreground">
        {emptyText || 'No data'}
      </p>
    );
  }

  return (
    <div className="overflow-x-auto">
      <Table>
        <TableHeader>
          <TableRow className="border-b border-border text-left text-xs text-muted-foreground">
            {columns.map((col) => (
              <TableHead key={col.path} className="px-3 py-2 font-medium">
                {col.label}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row, i) => (
            <TableRow key={i} className="border-b border-border/50 last:border-0">
              {columns.map((col) => (
                <Cell key={col.path} row={row} field={col} />
              ))}
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
