/**
 * CSV export for DataTable. Exports exactly the rows the caller passes (the
 * filtered, sorted row model) and the visible columns, in display order.
 */

export interface CsvColumn<T> {
  header: string;
  /** Full name; preferred over the short visible `header` in the export. */
  ariaLabel?: string;
  accessor: (row: T) => unknown;
  searchAccessor?: (row: T) => string;
  sortAccessor?: (row: T) => string | number;
}

/** Plain text for a cell: search text, then sort value, then a primitive render. */
export function columnText<T>(column: CsvColumn<T>, row: T): string {
  const value = column.searchAccessor
    ? column.searchAccessor(row)
    : column.sortAccessor
      ? column.sortAccessor(row)
      : column.accessor(row);
  if (value === null || value === undefined || typeof value === "boolean") {
    return "";
  }
  if (typeof value === "string" || typeof value === "number") {
    return String(value);
  }
  return "";
}

const NUMERIC = /^[-+]?\d+(?:\.\d+)?(?:e[-+]?\d+)?$/i;

/**
 * Escape one field per RFC 4180 and neutralize spreadsheet formulas: text
 * that begins with `=`, `+`, `-`, `@`, tab or CR gets a leading apostrophe
 * (plain numbers are left alone).
 */
export function csvField(value: string): string {
  let text = value;
  if (/^[=+\-@\t\r]/.test(text) && !NUMERIC.test(text)) text = `'${text}`;
  return /[",\r\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

export function toCsv<T>(
  columns: ReadonlyArray<CsvColumn<T>>,
  rows: ReadonlyArray<T>,
): string {
  const lines = [
    columns.map((c) => csvField(c.ariaLabel ?? c.header)).join(","),
  ];
  for (const row of rows) {
    lines.push(columns.map((c) => csvField(columnText(c, row))).join(","));
  }
  return lines.join("\r\n") + "\r\n";
}

/** Trigger a browser download. A BOM keeps non-ASCII intact in Excel. */
export function downloadCsv(filename: string, csv: string): void {
  const blob = new Blob(["﻿", csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  URL.revokeObjectURL(url);
}
