/** Virtualized ARIA-grid body for DataTable. */

import type React from "react";
import type {
  Row as RtRow,
  Table as RtTable,
  RowData,
} from "@tanstack/react-table";
import type { DataTableFeatures } from "./data-table-features";
import { ChevronDown, ChevronUp, ChevronsUpDown } from "lucide-react";
import { DataTableQueryError } from "@/components/ui/data-table-query-error";
import {
  TableEmptyPanel,
  type TableEmptyState,
} from "@/components/ui/data-table-empty-state";
import type { Column } from "@/components/ui/data-table";
import type { VirtualRows } from "@/components/ui/use-virtual-rows";
import { eventStartedInRowAction } from "@/components/ui/data-table-row-actions";
import { cn } from "@/lib/utils";

export function VirtualizedGrid<T extends RowData>({
  activeColumns,
  table,
  rows,
  rowVirtualizer,
  scrollRef,
  totalRows,
  selectable,
  resizable,
  cellPadding,
  selectPadding,
  rowHeight,
  loading,
  skeletonRows,
  emptyState,
  isError,
  error,
  permission,
  errorMessage,
  onRetry,
  keyExtractor,
  onRowClick,
  focusedRowIndex,
  setFocusedRowIndex,
  focusRowAt,
}: {
  activeColumns: Column<T>[];
  table: RtTable<DataTableFeatures, T>;
  rows: RtRow<DataTableFeatures, T>[];
  rowVirtualizer: VirtualRows;
  scrollRef: React.RefObject<HTMLDivElement | null>;
  totalRows: number;
  selectable: boolean | ((row: T) => boolean);
  resizable: boolean;
  cellPadding: string;
  selectPadding: string;
  rowHeight: number;
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
  focusedRowIndex: number;
  setFocusedRowIndex: (i: number) => void;
  focusRowAt: (i: number) => void;
}) {
  // Per-column width style shared by header + body cells so they line up.
  const colStyle = (col: Column<T>): React.CSSProperties => {
    const width = resizable
      ? `${table.getColumn(col.key)?.getSize()}px`
      : col.width;
    return width
      ? { width, flex: `0 0 ${width}`, minWidth: width }
      : { flex: "1 1 0", minWidth: 0 };
  };
  const selectColStyle: React.CSSProperties = {
    flex: "0 0 2.5rem",
    width: "2.5rem",
  };

  const alignClass = (col: Column<T>) =>
    cn(
      col.align === "center" && "text-center justify-center",
      col.align === "right" && "text-right justify-end",
    );

  const virtualItems = rowVirtualizer.items;

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <div
        ref={scrollRef}
        role="grid"
        aria-rowcount={totalRows}
        aria-colcount={activeColumns.length + (selectable ? 1 : 0)}
        aria-multiselectable={selectable ? true : undefined}
        // The grid container is the single Tab entry point. Rows are focused
        // programmatically (arrow keys / click) and stay out of the Tab order,
        // so the grid is reachable even when the previously-focused row has been
        // virtualized out of the DOM. Arrow keys here move focus into the body.
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget) return;
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            focusRowAt(focusedRowIndex >= 0 ? focusedRowIndex : 0);
          }
        }}
        className="relative max-h-[28rem] overflow-auto text-sm outline-hidden focus:ring-1 focus:ring-inset focus:ring-ring"
      >
        {/* Sticky header row */}
        <div
          role="row"
          aria-rowindex={1}
          className="sticky top-0 z-10 flex border-b border-border bg-muted/50 text-muted-foreground"
        >
          {selectable && (
            <div
              role="columnheader"
              className={cn("flex items-center", selectPadding)}
              style={selectColStyle}
            >
              <input
                type="checkbox"
                aria-label="Select all rows on this page"
                checked={table.getIsAllPageRowsSelected()}
                onChange={table.getToggleAllPageRowsSelectedHandler()}
                className="rounded-sm border-border text-primary focus:ring-ring"
              />
            </div>
          )}
          {activeColumns.map((col) => {
            const column = table.getColumn(col.key);
            const sorted = column?.getIsSorted();
            return (
              <div
                key={col.key}
                role="columnheader"
                aria-sort={
                  col.sortable !== false
                    ? sorted === "asc"
                      ? "ascending"
                      : sorted === "desc"
                        ? "descending"
                        : "none"
                    : undefined
                }
                className={cn(
                  cellPadding,
                  "flex items-center gap-1 font-medium whitespace-nowrap",
                  col.sortable !== false &&
                    "cursor-pointer select-none hover:text-foreground",
                  alignClass(col),
                )}
                style={colStyle(col)}
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
            );
          })}
        </div>

        {/* Body */}
        {isError ? (
          <div role="row">
            <div role="gridcell" className="px-4 py-12 text-center">
              <div className="sticky left-0 w-[calc(100vw-2rem)] max-w-full">
                <DataTableQueryError
                  error={error}
                  errorMessage={errorMessage}
                  onRetry={onRetry}
                  permission={permission}
                />
              </div>
            </div>
          </div>
        ) : loading ? (
          <div>
            {Array.from({ length: skeletonRows }).map((_, i) => (
              <div
                key={i}
                role="row"
                className="flex border-b border-border"
                style={{ height: rowHeight }}
              >
                {selectable && (
                  <div
                    role="gridcell"
                    className={cn("flex items-center", selectPadding)}
                    style={selectColStyle}
                  >
                    <div className="h-4 w-4 rounded-sm bg-muted animate-pulse" />
                  </div>
                )}
                {activeColumns.map((col) => (
                  <div
                    key={col.key}
                    role="gridcell"
                    className={cn("flex items-center", cellPadding)}
                    style={colStyle(col)}
                  >
                    <div
                      className="h-4 w-24 max-w-full rounded-sm bg-muted animate-pulse"
                      style={{
                        width: col.width
                          ? `min(100%, ${col.width})`
                          : undefined,
                      }}
                    />
                  </div>
                ))}
              </div>
            ))}
          </div>
        ) : rows.length === 0 ? (
          <div role="row">
            <div
              role="gridcell"
              className="px-4 py-12 text-center text-muted-foreground"
            >
              <div className="sticky left-0 w-[calc(100vw-2rem)] max-w-full">
                <TableEmptyPanel state={emptyState} />
              </div>
            </div>
          </div>
        ) : (
          <div
            style={{
              height: rowVirtualizer.totalSize,
              position: "relative",
              width: "100%",
            }}
          >
            {virtualItems.map((virtualRow) => {
              const row = rows[virtualRow.index];
              const key = keyExtractor(row.original);
              const isSelected = row.getIsSelected();
              return (
                <div
                  key={key}
                  // measureElement reads each row's real height for variable-size
                  // rows; data-index lets the virtualizer key the measurement.
                  ref={rowVirtualizer.measureElement}
                  data-index={virtualRow.index}
                  data-row-index={virtualRow.index}
                  role="row"
                  // 1-based, and +1 again because the sticky header is row 1.
                  aria-rowindex={virtualRow.index + 2}
                  aria-selected={selectable ? isSelected : undefined}
                  // Programmatically focusable only (-1): the grid container owns
                  // the Tab stop, so a virtualized-out focused row can't strand
                  // keyboard users outside the grid.
                  tabIndex={-1}
                  onFocus={() => setFocusedRowIndex(virtualRow.index)}
                  onKeyDown={(e) => {
                    if (e.key === "ArrowDown") {
                      e.preventDefault();
                      focusRowAt(virtualRow.index + 1);
                    } else if (e.key === "ArrowUp") {
                      e.preventDefault();
                      focusRowAt(virtualRow.index - 1);
                    } else if (
                      e.target === e.currentTarget &&
                      e.key === "Enter" &&
                      onRowClick
                    ) {
                      e.preventDefault();
                      onRowClick(row.original);
                    }
                  }}
                  onClick={(event) => {
                    if (!eventStartedInRowAction(event))
                      onRowClick?.(row.original);
                  }}
                  className={cn(
                    "absolute left-0 top-0 flex w-full border-b border-border transition-colors",
                    "focus:outline-hidden focus:ring-1 focus:ring-inset focus:ring-ring",
                    onRowClick && "cursor-pointer hover:bg-muted/50",
                    isSelected && "bg-muted/30",
                  )}
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                >
                  {selectable && (
                    <div
                      role="gridcell"
                      className={cn("flex items-center", selectPadding)}
                      style={selectColStyle}
                    >
                      <input
                        type="checkbox"
                        aria-label={`Select row ${keyExtractor(row.original)}`}
                        checked={isSelected}
                        disabled={!row.getCanSelect()}
                        onChange={row.getToggleSelectedHandler()}
                        className="rounded-sm border-border text-primary focus:ring-ring disabled:cursor-not-allowed disabled:opacity-40"
                      />
                    </div>
                  )}
                  {activeColumns.map((col) => (
                    <div
                      key={col.key}
                      role="gridcell"
                      className={cn(
                        "flex items-center",
                        cellPadding,
                        alignClass(col),
                      )}
                      style={colStyle(col)}
                    >
                      {col.accessor(row.original)}
                    </div>
                  ))}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
