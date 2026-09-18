import { useMemo, type ReactNode } from "react";
import type { RowData, Table as RtTable } from "@tanstack/react-table";

import type { Column } from "@/components/ui/data-table";
import {
  TableEmptyPanel,
  type TableEmptyState,
} from "@/components/ui/data-table-empty-state";
import { DataTableQueryError } from "@/components/ui/data-table-query-error";
import { eventStartedInRowAction } from "@/components/ui/data-table-row-actions";
import type { DataTableFeatures } from "@/components/ui/data-table-features";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { ChevronDown, ChevronUp, ChevronsUpDown } from "lucide-react";

interface SemanticDataTableProps<T extends RowData> {
  table: RtTable<DataTableFeatures, T>;
  activeColumns: Column<T>[];
  selectable: boolean | ((row: T) => boolean);
  resizable: boolean;
  cellPadding: string;
  selectPadding: string;
  loading: boolean;
  skeletonRows: number;
  emptyState: TableEmptyState;
  isError: boolean;
  error?: unknown;
  permission?: string;
  errorMessage: string;
  onRetry?: () => void;
  keyExtractor: (row: T) => string;
  onRowClick?: (row: T) => void;
}

export function SemanticDataTable<T extends RowData>({
  table,
  activeColumns,
  selectable,
  resizable,
  cellPadding,
  selectPadding,
  loading,
  skeletonRows,
  emptyState: filteredEmptyState,
  isError,
  error,
  permission,
  errorMessage,
  onRetry,
  keyExtractor,
  onRowClick,
}: SemanticDataTableProps<T>): ReactNode {
  const rows = table.getRowModel().rows;
  const headerByKey = useMemo(
    () =>
      new Map(
        table
          .getHeaderGroups()[0]
          ?.headers.map((header) => [header.column.id, header]),
      ),
    [table],
  );

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <div
        className="overflow-x-auto focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
        role="region"
        aria-label="Scrollable data table"
        tabIndex={0}
      >
        <Table className="w-full text-sm">
          <TableHeader>
            <TableRow className="border-b border-border bg-muted/50">
              {selectable && (
                <TableHead className={cn("w-10", selectPadding)}>
                  <input
                    type="checkbox"
                    aria-label="Select all rows on this page"
                    checked={table.getIsAllPageRowsSelected()}
                    onChange={table.getToggleAllPageRowsSelectedHandler()}
                    className="rounded-sm border-border text-primary focus:ring-ring"
                  />
                </TableHead>
              )}
              {activeColumns.map((col) => {
                const column = table.getColumn(col.key);
                const sorted = column?.getIsSorted();
                const header = resizable ? headerByKey.get(col.key) : undefined;
                return (
                  <TableHead
                    key={col.key}
                    className={cn(
                      cellPadding,
                      "font-medium text-muted-foreground whitespace-nowrap",
                      resizable && "relative",
                      col.align === "center" && "text-center",
                      col.align === "right" && "text-right",
                      col.sortable !== false &&
                        "cursor-pointer select-none hover:text-foreground",
                    )}
                    style={
                      resizable
                        ? { width: column?.getSize() }
                        : col.width
                          ? { width: col.width }
                          : undefined
                    }
                    aria-sort={
                      col.sortable !== false
                        ? sorted === "asc"
                          ? "ascending"
                          : sorted === "desc"
                            ? "descending"
                            : "none"
                        : undefined
                    }
                    tabIndex={col.sortable !== false ? 0 : undefined}
                    onClick={() =>
                      col.sortable !== false && column?.toggleSorting()
                    }
                    onKeyDown={(event) => {
                      if (
                        col.sortable === false ||
                        (event.key !== "Enter" && event.key !== " ")
                      )
                        return;
                      event.preventDefault();
                      column?.toggleSorting();
                    }}
                  >
                    <div
                      className={cn(
                        "flex items-center gap-1",
                        col.align === "center" && "justify-center",
                        col.align === "right" && "justify-end",
                      )}
                    >
                      {col.header}
                      {col.sortable !== false && (
                        <span className="text-muted-foreground/50">
                          {sorted === "asc" ? (
                            <ChevronUp className="h-3.5 w-3.5" />
                          ) : sorted === "desc" ? (
                            <ChevronDown className="h-3.5 w-3.5" />
                          ) : (
                            <ChevronsUpDown className="h-3 w-3" />
                          )}
                        </span>
                      )}
                    </div>
                    {resizable && header?.column.getCanResize() && (
                      <button
                        type="button"
                        aria-label={`Resize ${col.header} column, currently ${header.column.getSize()} pixels. Use left and right arrow keys.`}
                        tabIndex={0}
                        data-resize-handle=""
                        onMouseDown={header.getResizeHandler()}
                        onTouchStart={header.getResizeHandler()}
                        onClick={(e) => e.stopPropagation()}
                        onKeyDown={(event) => {
                          event.stopPropagation();
                          if (
                            event.key !== "ArrowLeft" &&
                            event.key !== "ArrowRight"
                          )
                            return;
                          event.preventDefault();
                          const delta = event.key === "ArrowRight" ? 16 : -16;
                          table.setColumnSizing((current) => ({
                            ...current,
                            [header.column.id]: Math.max(
                              48,
                              header.column.getSize() + delta,
                            ),
                          }));
                        }}
                        className={cn(
                          "absolute right-0 top-0 h-full w-1 cursor-col-resize select-none touch-none",
                          "bg-transparent hover:bg-border/80",
                          header.column.getIsResizing() && "bg-primary/60",
                        )}
                      />
                    )}
                  </TableHead>
                );
              })}
            </TableRow>
          </TableHeader>
          <TableBody>
            {isError ? (
              <TableRow>
                <TableCell
                  colSpan={activeColumns.length + (selectable ? 1 : 0)}
                  className="px-4 py-12 text-center"
                >
                  <div className="sticky left-0 w-[calc(100vw-2rem)] max-w-full">
                    <DataTableQueryError
                      error={error}
                      errorMessage={errorMessage}
                      onRetry={onRetry}
                      permission={permission}
                    />
                  </div>
                </TableCell>
              </TableRow>
            ) : loading ? (
              Array.from({ length: skeletonRows }).map((_, i) => (
                <TableRow
                  key={i}
                  className="border-b border-border last:border-0"
                >
                  {selectable && (
                    <TableCell className={selectPadding}>
                      <div className="h-4 w-4 rounded-sm bg-muted animate-pulse" />
                    </TableCell>
                  )}
                  {activeColumns.map((col) => (
                    <TableCell
                      key={col.key}
                      className={cellPadding}
                      style={
                        resizable
                          ? { width: table.getColumn(col.key)?.getSize() }
                          : undefined
                      }
                    >
                      <div
                        className="h-4 w-24 max-w-full rounded-sm bg-muted animate-pulse"
                        style={{
                          width: col.width
                            ? `min(100%, ${col.width})`
                            : undefined,
                        }}
                      />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={activeColumns.length + (selectable ? 1 : 0)}
                  className="px-4 py-12 text-center text-muted-foreground"
                >
                  <div className="sticky left-0 w-[calc(100vw-2rem)] max-w-full">
                    <TableEmptyPanel state={filteredEmptyState} />
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row) => {
                const key = keyExtractor(row.original);
                const isSelected = row.getIsSelected();
                return (
                  <TableRow
                    key={key}
                    className={cn(
                      "border-b border-border last:border-0 whitespace-nowrap transition-colors",
                      onRowClick && "cursor-pointer hover:bg-muted/50",
                      isSelected && "bg-muted/30",
                    )}
                    tabIndex={onRowClick ? 0 : undefined}
                    onKeyDown={(event) => {
                      if (!onRowClick || event.target !== event.currentTarget)
                        return;
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onRowClick(row.original);
                      }
                    }}
                    onClick={(event) => {
                      if (!eventStartedInRowAction(event))
                        onRowClick?.(row.original);
                    }}
                  >
                    {selectable && (
                      <TableCell className={selectPadding}>
                        <input
                          type="checkbox"
                          aria-label={`Select row ${keyExtractor(row.original)}`}
                          checked={isSelected}
                          disabled={!row.getCanSelect()}
                          onChange={row.getToggleSelectedHandler()}
                          className="rounded-sm border-border text-primary focus:ring-ring disabled:cursor-not-allowed disabled:opacity-40"
                        />
                      </TableCell>
                    )}
                    {activeColumns.map((col) => (
                      <TableCell
                        key={col.key}
                        className={cn(
                          cellPadding,
                          "whitespace-nowrap",
                          col.align === "center" && "text-center",
                          col.align === "right" && "text-right",
                        )}
                        style={
                          resizable
                            ? { width: table.getColumn(col.key)?.getSize() }
                            : undefined
                        }
                      >
                        {col.accessor(row.original)}
                      </TableCell>
                    ))}
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
