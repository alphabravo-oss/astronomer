/** Virtualized ARIA-grid body for DataTable. */

import type React from "react";
import type {
  Row as RtRow,
  Table as RtTable,
  RowData,
} from "@tanstack/react-table";
import type { DataTableFeatures } from "./data-table-features";
import {
  ChevronDown,
  ChevronRight,
  ChevronUp,
  ChevronsUpDown,
} from "lucide-react";
import { DataTableCellContent } from "@/components/ui/data-table-cell";
import { columnText } from "@/components/ui/data-table-csv";
import {
  flexCellStyle,
  SELECT_COLUMN_WIDTH,
  type PinnedPlacement,
  type ResolvedColumnLayout,
} from "@/components/ui/data-table-layout";
import { Skeleton } from "@/components/ui/skeleton";
import { DataTableQueryError } from "@/components/ui/data-table-query-error";
import {
  TableEmptyPanel,
  type TableEmptyState,
} from "@/components/ui/data-table-empty-state";
import type { Column } from "@/components/ui/data-table";
import type { VirtualRows } from "@/components/ui/use-virtual-rows";
import { eventStartedInRowAction } from "@/components/ui/data-table-row-actions";
import { cn } from "@/lib/utils";
import { Checkbox } from "@/components/ui/checkbox";
import { tableGroupPositions } from "./data-table-grouping";

export function VirtualizedGrid<T extends RowData>({
  groupBy,
  activeColumns,
  layouts,
  placements,
  expandable = false,
  renderSubRow,
  table,
  rows,
  rowVirtualizer,
  scrollRef,
  selectable,
  resizable,
  layout,
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
  groupBy?: (row: T) => string;
  activeColumns: Column<T>[];
  layouts: Map<string, ResolvedColumnLayout>;
  placements: Map<string, PinnedPlacement>;
  expandable?: boolean;
  renderSubRow?: (row: T) => React.ReactNode;
  table: RtTable<DataTableFeatures, T>;
  rows: RtRow<DataTableFeatures, T>[];
  rowVirtualizer: VirtualRows;
  scrollRef: React.RefObject<HTMLDivElement | null>;
  totalRows: number;
  selectable: boolean | ((row: T) => boolean);
  resizable: boolean;
  layout: "fit" | "scroll";
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
  const scrolls = layout === "scroll";
  const stickyBg = scrolls ? "bg-background" : undefined;
  const colStyle = (col: Column<T>): React.CSSProperties => {
    const resolved = layouts.get(col.key);
    if (!resolved) return { flex: "1 1 0", minWidth: 0 };
    const stored = (
      table.options.state as { columnSizing?: Record<string, number> }
    ).columnSizing?.[col.key];
    const resizedWidth = resizable
      ? resolved.sized
        ? stored
        : (stored ?? table.getColumn(col.key)?.getSize())
      : undefined;
    return flexCellStyle(resolved, {
      resizedWidth,
      pinned: placements.get(col.key),
      scroll: scrolls,
    });
  };
  const selectColStyle: React.CSSProperties = {
    flex: "0 0 2.5rem",
    width: "2.5rem",
    position: "sticky",
    left: 0,
    zIndex: "var(--z-sticky)" as unknown as number,
  };
  const expandColStyle: React.CSSProperties = {
    flex: "0 0 2.5rem",
    width: "2.5rem",
    position: "sticky",
    left: selectable ? SELECT_COLUMN_WIDTH : 0,
    zIndex: "var(--z-sticky)" as unknown as number,
  };
  const leadCount = (selectable ? 1 : 0) + (expandable ? 1 : 0);
  const pinnedClass = (col: Column<T>) =>
    placements.has(col.key) ? stickyBg : undefined;

  const alignClass = (col: Column<T>) => {
    const align = layouts.get(col.key)?.align;
    return cn(
      align === "center" && "text-center justify-center",
      align === "right" && "text-right justify-end",
    );
  };

  const virtualItems = rowVirtualizer.items;
  const groups = tableGroupPositions(
    rows,
    groupBy ? (row) => groupBy(row.original) : undefined,
  );

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <div
        ref={scrollRef}
        role="grid"
        aria-rowcount={groups.rowCount}
        aria-colcount={activeColumns.length + leadCount}
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
            focusRowAt(Math.min(Math.max(focusedRowIndex, 0), rows.length - 1));
          }
        }}
        className={cn(
          "relative max-h-[28rem] overflow-y-auto text-sm outline-hidden focus:ring-1 focus:ring-inset focus:ring-ring",
          layout === "scroll" ? "overflow-x-auto" : "overflow-x-hidden",
        )}
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
              className={cn(
                "flex items-center",
                scrolls && "bg-muted",
                selectPadding,
              )}
              style={selectColStyle}
            >
              <Checkbox
                aria-label="Select all rows on this page"
                checked={table.getIsAllPageRowsSelected()}
                onChange={table.getToggleAllPageRowsSelectedHandler()}
              />
            </div>
          )}
          {expandable && (
            <div
              role="columnheader"
              className={cn(
                "flex items-center",
                scrolls && "bg-muted",
                selectPadding,
              )}
              style={expandColStyle}
            >
              <span className="sr-only">Expand row</span>
            </div>
          )}
          {activeColumns.map((col) => {
            const column = table.getColumn(col.key);
            const rowActions = col.rowActions === true;
            const sortable = !rowActions && col.sortable !== false;
            const sorted = column?.getIsSorted();
            return (
              <div
                key={col.key}
                role="columnheader"
                aria-sort={
                  sortable
                    ? sorted === "asc"
                      ? "ascending"
                      : sorted === "desc"
                        ? "descending"
                        : "none"
                    : undefined
                }
                className={cn(
                  cellPadding,
                  "flex min-w-0 items-center gap-1 overflow-hidden font-medium",
                  rowActions && "px-1.5",
                  sortable &&
                    "cursor-pointer select-none hover:text-foreground",
                  alignClass(col),
                  placements.has(col.key) && scrolls && "bg-muted",
                )}
                style={colStyle(col)}
                tabIndex={sortable ? 0 : undefined}
                onClick={() => sortable && column?.toggleSorting()}
                onKeyDown={(event) => {
                  if (!sortable || (event.key !== "Enter" && event.key !== " "))
                    return;
                  event.preventDefault();
                  column?.toggleSorting();
                }}
              >
                <span
                  className={cn(
                    layouts.get(col.key)?.sized
                      ? "whitespace-nowrap"
                      : "min-w-0 truncate",
                    rowActions && "sr-only",
                  )}
                >
                  {col.header || (rowActions ? "Actions" : "")}
                </span>
                {sortable && (
                  <span className="shrink-0 text-muted-foreground/50">
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
                    <Skeleton className="h-4 w-4 rounded-sm" />
                  </div>
                )}
                {expandable && (
                  <div
                    role="gridcell"
                    className={cn("flex items-center", selectPadding)}
                    style={expandColStyle}
                  >
                    <Skeleton className="h-4 w-4 rounded-sm" />
                  </div>
                )}
                {activeColumns.map((col) => {
                  const rowActions = col.rowActions === true;
                  return (
                    <div
                      key={col.key}
                      role="gridcell"
                      className={cn(
                        "flex items-center",
                        cellPadding,
                        rowActions && "px-1.5",
                      )}
                      style={colStyle(col)}
                    >
                      <Skeleton
                        className="h-4 w-24 max-w-full rounded-sm"
                        style={{
                          width: col.width
                            ? `min(100%, ${col.width})`
                            : undefined,
                        }}
                      />
                    </div>
                  );
                })}
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
              // The virtualizer publishes after commit; a filter can shrink
              // the row model before its previous window has been replaced.
              if (!row) return null;
              const key = keyExtractor(row.original);
              const isSelected = row.getIsSelected();
              const group = groups.positions[virtualRow.index];
              return (
                <div
                  key={key}
                  ref={rowVirtualizer.measureElement}
                  data-index={virtualRow.index}
                  className="absolute left-0 top-0 w-full"
                  style={{ transform: `translateY(${virtualRow.start}px)` }}
                >
                  {group.start && (
                    <div
                      role="row"
                      aria-rowindex={group.rowIndex - 1}
                      className="bg-muted px-3 py-2 text-xs font-semibold"
                    >
                      <div
                        role="rowheader"
                        aria-colspan={activeColumns.length + leadCount}
                      >
                        {group.label}
                      </div>
                    </div>
                  )}
                  <div
                    key={key}
                    // measureElement reads each row's real height for variable-size
                    // rows; data-index lets the virtualizer key the measurement.
                    data-row-index={virtualRow.index}
                    role="row"
                    // 1-based, and +1 again because the sticky header is row 1.
                    aria-rowindex={group.rowIndex}
                    aria-selected={selectable ? isSelected : undefined}
                    // Programmatically focusable only (-1): the grid container owns
                    // the Tab stop, so a virtualized-out focused row can't strand
                    // keyboard users outside the grid.
                    tabIndex={-1}
                    aria-expanded={expandable ? row.getIsExpanded() : undefined}
                    onFocus={() => setFocusedRowIndex(virtualRow.index)}
                    onKeyDown={(e) => {
                      if (e.target !== e.currentTarget) return;
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
                      "flex w-full border-b border-border transition-colors",
                      "focus:outline-hidden focus:ring-1 focus:ring-inset focus:ring-ring",
                      onRowClick && "cursor-pointer hover:bg-muted/50",
                      isSelected && "bg-muted/30",
                    )}
                  >
                    {selectable && (
                      <div
                        role="gridcell"
                        className={cn(
                          "flex items-center",
                          selectPadding,
                          stickyBg,
                        )}
                        style={selectColStyle}
                      >
                        <Checkbox
                          aria-label={`Select row ${keyExtractor(row.original)}`}
                          checked={isSelected}
                          disabled={!row.getCanSelect()}
                          onChange={row.getToggleSelectedHandler()}
                        />
                      </div>
                    )}
                    {expandable && (
                      <div
                        role="gridcell"
                        className={cn(
                          "flex items-center px-1.5",
                          selectPadding,
                          stickyBg,
                        )}
                        style={expandColStyle}
                      >
                        <button
                          type="button"
                          aria-label={`${row.getIsExpanded() ? "Collapse" : "Expand"} row ${key}`}
                          aria-expanded={row.getIsExpanded()}
                          aria-controls={`subrow-${key}`}
                          onClick={(event) => {
                            event.stopPropagation();
                            row.toggleExpanded();
                          }}
                          className="inline-flex h-6 w-6 items-center justify-center rounded-sm text-muted-foreground hover:bg-accent hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring"
                        >
                          <ChevronRight
                            className={cn(
                              "h-4 w-4 transition-transform",
                              row.getIsExpanded() && "rotate-90",
                            )}
                          />
                        </button>
                      </div>
                    )}
                    {activeColumns.map((col) => {
                      const rowActions = col.rowActions === true;
                      const resolved = layouts.get(col.key);
                      const overflow = resolved?.overflow ?? "legacy";
                      return (
                        <div
                          key={col.key}
                          role="gridcell"
                          className={cn(
                            "flex min-w-0 items-center overflow-hidden",
                            pinnedClass(col),
                            cellPadding,
                            rowActions && "px-1.5",
                            alignClass(col),
                          )}
                          style={colStyle(col)}
                        >
                          <DataTableCellContent
                            overflow={rowActions ? "fixed" : overflow}
                            mono={resolved?.mono === true}
                            numeric={resolved?.numeric === true}
                            label={col.header}
                            text={
                              overflow === "middle"
                                ? columnText(col, row.original)
                                : ""
                            }
                            width={resolved?.size}
                          >
                            {col.accessor(row.original)}
                          </DataTableCellContent>
                        </div>
                      );
                    })}
                  </div>
                  {expandable && row.getIsExpanded() ? (
                    <div
                      role="row"
                      id={`subrow-${key}`}
                      data-subrow=""
                      className="border-b border-border bg-muted/20"
                    >
                      <div
                        role="gridcell"
                        aria-colspan={activeColumns.length + leadCount}
                        className="px-4 py-3 text-sm"
                      >
                        {renderSubRow?.(row.original)}
                      </div>
                    </div>
                  ) : null}
                </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
