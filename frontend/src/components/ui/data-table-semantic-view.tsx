import { Fragment, useMemo, type CSSProperties, type ReactNode } from "react";
import type { RowData, Row, Table as RtTable } from "@tanstack/react-table";

import type { Column } from "@/components/ui/data-table";
import { DataTableCellContent } from "@/components/ui/data-table-cell";
import { columnText } from "@/components/ui/data-table-csv";
import {
  TableEmptyPanel,
  type TableEmptyState,
} from "@/components/ui/data-table-empty-state";
import {
  SELECT_COLUMN_WIDTH,
  minTableWidth,
  tableCellStyle,
  type PinnedPlacement,
  type ResolvedColumnLayout,
} from "@/components/ui/data-table-layout";
import { DataTableQueryError } from "@/components/ui/data-table-query-error";
import { eventStartedInRowAction } from "@/components/ui/data-table-row-actions";
import type { DataTableFeatures } from "@/components/ui/data-table-features";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import {
  ChevronDown,
  ChevronRight,
  ChevronUp,
  ChevronsUpDown,
} from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";

interface SemanticDataTableProps<T extends RowData> {
  rows: Row<DataTableFeatures, T>[];
  groupBy?: (row: T) => string;
  table: RtTable<DataTableFeatures, T>;
  activeColumns: Column<T>[];
  layouts: Map<string, ResolvedColumnLayout>;
  placements: Map<string, PinnedPlacement>;
  selectable: boolean | ((row: T) => boolean);
  expandable?: boolean;
  renderSubRow?: (row: T) => ReactNode;
  keyboardNav?: boolean;
  resizable: boolean;
  layout: "fit" | "scroll";
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

const EXPAND_COLUMN_WIDTH = 40;

export function SemanticDataTable<T extends RowData>({
  rows,
  groupBy,
  table,
  activeColumns,
  layouts,
  placements,
  selectable,
  expandable = false,
  renderSubRow,
  keyboardNav = false,
  resizable,
  layout,
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
  const headerByKey = useMemo(
    () =>
      new Map(
        table
          .getHeaderGroups()[0]
          ?.headers.map((header) => [header.column.id, header]),
      ),
    [table],
  );
  // Sticky cells only need an opaque background when the table can scroll
  // sideways; in fit layout they keep the row's own hover/selected tint.
  const scrolls = layout === "scroll";
  const widthFloor = minTableWidth(
    activeColumns.map((col) => layouts.get(col.key)),
    (selectable ? 40 : 0) + (expandable ? 40 : 0),
  );
  const stickyBg = scrolls ? "bg-background" : undefined;
  const pinnedClasses = (
    placement: PinnedPlacement | undefined,
    header: boolean,
  ) =>
    placement
      ? cn(
          header ? scrolls && "bg-muted" : stickyBg,
          placement.edge &&
            scrolls &&
            (placement.side === "start"
              ? "border-r border-border"
              : "border-l border-border"),
        )
      : undefined;
  const leadCount = (selectable ? 1 : 0) + (expandable ? 1 : 0);
  const colSpan = activeColumns.length + leadCount;
  const expandLeft = selectable ? SELECT_COLUMN_WIDTH : 0;
  const leadStyle = (left: number, width: number): CSSProperties => ({
    position: "sticky",
    left,
    width,
    minWidth: width,
    maxWidth: width,
    zIndex: "var(--z-sticky)" as unknown as number,
  });
  const resizedWidth = (col: Column<T>): number | undefined => {
    if (!resizable) return undefined;
    const resolved = layouts.get(col.key);
    const stored = (
      table.options.state as { columnSizing?: Record<string, number> }
    ).columnSizing?.[col.key];
    return resolved?.sized
      ? stored
      : (stored ?? table.getColumn(col.key)?.getSize());
  };
  const styleFor = (col: Column<T>): CSSProperties | undefined => {
    const resolved = layouts.get(col.key);
    if (!resolved) return undefined;
    const style = tableCellStyle(resolved, {
      resizedWidth: resizedWidth(col),
      pinned: placements.get(col.key),
    });
    return Object.keys(style).length ? style : undefined;
  };

  return (
    <div className="rounded-lg border border-border overflow-hidden">
      <div
        className={cn(
          "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
          scrolls || widthFloor !== undefined
            ? "overflow-x-auto"
            : "overflow-x-hidden",
        )}
        role="region"
        aria-label={
          layout === "scroll" ? "Scrollable data table" : "Data table"
        }
        tabIndex={0}
        data-table-region=""
      >
        <Table
          layout={layout}
          className="w-full text-sm"
          style={widthFloor ? { minWidth: widthFloor } : undefined}
        >
          <TableHeader>
            <TableRow className="border-b border-border bg-muted/50">
              {selectable && (
                <TableHead
                  className={cn("w-10", scrolls && "bg-muted", selectPadding)}
                  style={leadStyle(0, SELECT_COLUMN_WIDTH)}
                >
                  <Checkbox
                    aria-label="Select all rows on this page"
                    checked={table.getIsAllPageRowsSelected()}
                    onChange={table.getToggleAllPageRowsSelectedHandler()}
                  />
                </TableHead>
              )}
              {expandable && (
                <TableHead
                  className={cn("px-1.5", scrolls && "bg-muted", selectPadding)}
                  style={leadStyle(expandLeft, EXPAND_COLUMN_WIDTH)}
                >
                  <span className="sr-only">Expand row</span>
                </TableHead>
              )}
              {activeColumns.map((col) => {
                const column = table.getColumn(col.key);
                const resolved = layouts.get(col.key);
                const placement = placements.get(col.key);
                const rowActions = col.rowActions === true;
                const sorted = column?.getIsSorted();
                const header = resizable ? headerByKey.get(col.key) : undefined;
                const sortable = !rowActions && col.sortable !== false;
                // Sized columns reserve room for the label, so it never clips.
                const keepWhole = resolved?.sized === true;
                const headerContent = (
                  <>
                    <span
                      className={cn(
                        keepWhole ? "whitespace-nowrap" : "min-w-0 truncate",
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
                  </>
                );
                const alignClass = cn(
                  resolved?.align === "center" && "justify-center",
                  resolved?.align === "right" && "justify-end",
                );
                return (
                  <TableHead
                    key={col.key}
                    className={cn(
                      cellPadding,
                      "min-w-0 overflow-hidden font-medium text-muted-foreground",
                      rowActions && "px-1.5",
                      resizable && "relative",
                      resolved?.align === "center" && "text-center",
                      resolved?.align === "right" && "text-right",
                      pinnedClasses(placement, true),
                    )}
                    style={styleFor(col)}
                    aria-sort={
                      sortable
                        ? sorted === "asc"
                          ? "ascending"
                          : sorted === "desc"
                            ? "descending"
                            : "none"
                        : undefined
                    }
                  >
                    {sortable ? (
                      <button
                        type="button"
                        aria-label={`Sort by ${col.header}`}
                        onClick={() => column?.toggleSorting()}
                        className={cn(
                          "flex min-w-0 items-center gap-1 overflow-hidden p-0 cursor-pointer select-none hover:text-foreground focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset",
                          // Narrower than the full header width when a resize
                          // handle shares this header, so the two adjacent
                          // touch targets have clear space between them
                          // instead of touching bounding boxes (WCAG 2.5.8
                          // target spacing) — a right-margin/padding trick
                          // doesn't work here because the browser resolves an
                          // over-constrained `width: 100%` + margin by
                          // discarding the margin.
                          resizable && header?.column.getCanResize()
                            ? "w-[calc(100%-12px)]"
                            : "w-full",
                          alignClass,
                        )}
                      >
                        {headerContent}
                      </button>
                    ) : (
                      <div
                        className={cn("flex items-center gap-1", alignClass)}
                      >
                        {headerContent}
                      </div>
                    )}
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
                <TableCell colSpan={colSpan} className="px-4 py-12 text-center">
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
                      <Skeleton className="h-4 w-4 rounded-sm" />
                    </TableCell>
                  )}
                  {expandable && (
                    <TableCell className={selectPadding}>
                      <Skeleton className="h-4 w-4 rounded-sm" />
                    </TableCell>
                  )}
                  {activeColumns.map((col) => {
                    const rowActions = col.rowActions === true;
                    return (
                      <TableCell
                        key={col.key}
                        className={cn(cellPadding, rowActions && "px-1.5")}
                        style={styleFor(col)}
                      >
                        <Skeleton
                          className="h-4 w-24 max-w-full rounded-sm"
                          style={{
                            width: col.width
                              ? `min(100%, ${col.width})`
                              : undefined,
                          }}
                        />
                      </TableCell>
                    );
                  })}
                </TableRow>
              ))
            ) : rows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={colSpan}
                  className="px-4 py-12 text-center text-muted-foreground"
                >
                  <div className="sticky left-0 w-[calc(100vw-2rem)] max-w-full">
                    <TableEmptyPanel state={filteredEmptyState} />
                  </div>
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row, index) => {
                const key = keyExtractor(row.original);
                const isSelected = row.getIsSelected();
                const isExpanded = expandable && row.getIsExpanded();
                const subRow = isExpanded ? renderSubRow?.(row.original) : null;
                return (
                  <Fragment key={key}>
                    {groupBy &&
                      (index === 0 ||
                        groupBy(row.original) !==
                          groupBy(rows[index - 1].original)) && (
                        <TableRow>
                          <TableHead
                            colSpan={colSpan}
                            className="bg-muted px-3 py-2 text-xs font-semibold"
                          >
                            {groupBy(row.original)}
                          </TableHead>
                        </TableRow>
                      )}
                    <TableRow
                      key={key}
                      data-row-index={index}
                      className={cn(
                        "border-b border-border last:border-0 transition-colors",
                        "focus-visible:outline-hidden focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring",
                        onRowClick && "cursor-pointer hover:bg-muted/50",
                        isSelected && "bg-muted/30",
                      )}
                      tabIndex={onRowClick ? 0 : keyboardNav ? -1 : undefined}
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
                        <TableCell
                          className={cn(selectPadding, stickyBg)}
                          style={leadStyle(0, SELECT_COLUMN_WIDTH)}
                        >
                          <Checkbox
                            aria-label={`Select row ${keyExtractor(row.original)}`}
                            checked={isSelected}
                            disabled={!row.getCanSelect()}
                            onChange={row.getToggleSelectedHandler()}
                          />
                        </TableCell>
                      )}
                      {expandable && (
                        <TableCell
                          className={cn("px-1.5", selectPadding, stickyBg)}
                          style={leadStyle(expandLeft, EXPAND_COLUMN_WIDTH)}
                        >
                          <button
                            type="button"
                            aria-label={`${isExpanded ? "Collapse" : "Expand"} row ${key}`}
                            aria-expanded={isExpanded}
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
                                isExpanded && "rotate-90",
                              )}
                            />
                          </button>
                        </TableCell>
                      )}
                      {activeColumns.map((col) => {
                        const resolved = layouts.get(col.key);
                        const placement = placements.get(col.key);
                        const rowActions = col.rowActions === true;
                        const overflow = resolved?.overflow ?? "legacy";
                        return (
                          <TableCell
                            key={col.key}
                            className={cn(
                              cellPadding,
                              "min-w-0 overflow-hidden",
                              rowActions && "px-1.5",
                              resolved?.align === "center" && "text-center",
                              resolved?.align === "right" && "text-right",
                              pinnedClasses(placement, false),
                            )}
                            style={styleFor(col)}
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
                          </TableCell>
                        );
                      })}
                    </TableRow>
                    {subRow ? (
                      <TableRow
                        id={`subrow-${key}`}
                        data-subrow=""
                        className="border-b border-border bg-muted/20 last:border-0"
                      >
                        <TableCell
                          colSpan={colSpan}
                          className="whitespace-normal px-4 py-3"
                        >
                          <div className="sticky left-4 w-[calc(100vw-4rem)] max-w-full whitespace-normal">
                            {subRow}
                          </div>
                        </TableCell>
                      </TableRow>
                    ) : null}
                  </Fragment>
                );
              })
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}
