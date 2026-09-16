import type { ReactNode } from "react";
import { type RowData, type PaginationState } from "@tanstack/react-table";
import { cn } from "@/lib/utils";
import type { TableEmptyState } from "@/components/ui/data-table-empty-state";

import { DataTableToolbar } from "@/components/ui/data-table-toolbar";
import { DataTablePagination } from "@/components/ui/data-table-pagination";
import { VirtualizedGrid } from "@/components/ui/data-table-virtualized-body";
import { SemanticDataTable } from "@/components/ui/data-table-semantic-view";
import { useDataTableController } from "@/components/ui/use-data-table-controller";

// ============================================================
// Types
// ============================================================

export interface Column<T> {
  key: string;
  header: string;
  accessor: (row: T) => React.ReactNode;
  /**
   * Plain text used by global search when the rendered cell is JSX. When it is
   * omitted, search falls back to `sortAccessor` and then the rendered value.
   */
  searchAccessor?: (row: T) => string;
  sortAccessor?: (row: T) => string | number;
  sortable?: boolean;
  filterable?: boolean;
  hidden?: boolean;
  width?: string;
  align?: "left" | "center" | "right";
  /**
   * When set, renders a faceted multi-select filter for this column in the
   * toolbar. The facet options are derived automatically from the column's
   * `sortAccessor` value (so faceted columns should define a `sortAccessor`
   * that returns the scalar to filter on).
   */
  filter?: { label?: string };
}

export interface DataTableProps<T> {
  data: T[];
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  density?: "compact" | "comfortable";
  searchable?: boolean;
  searchPlaceholder?: string;
  selectable?: boolean | ((row: T) => boolean);
  onRowClick?: (row: T) => void;
  onSelectionChange?: (selected: T[]) => void;
  bulkActions?: (selected: T[]) => ReactNode;
  pageSize?: number;
  loadingRows?: number;
  emptyState?: TableEmptyState;
  /** Include filters applied by the caller before rows reach this table. */
  filtersActive?: boolean;
  onClearFilters?: () => void;
  loading?: boolean;
  /**
   * When true, the table renders a single distinct error row (styled unlike the
   * empty state) instead of data/loading/empty. Pass `query.isError` here.
   */
  isError?: boolean;
  /** Raw query error, used to distinguish permission, offline, and API errors. */
  error?: unknown;
  /** Permission displayed for 401/403 table failures. */
  permission?: string;
  /** Message shown in the error row. */
  errorMessage?: string;
  /** Optional retry action; when provided, an inline Retry button is shown. */
  onRetry?: () => void;
  toolbar?: ReactNode;
  className?: string;
  /**
   * When set, the user's column-visibility choices are persisted to
   * localStorage under this key and restored on next mount. Omit for
   * ephemeral (non-persisted) tables.
   */
  persistKey?: string;
  /**
   * Opt into interactive column resizing. When false (the default), the table
   * renders identically to before — fixed widths come from each column's
   * `width`. When true, columns become drag-resizable and their pixel sizes are
   * persisted under `dt:<persistKey>:sizing` if `persistKey` is set.
   */
  resizable?: boolean;
  /**
   * Choose row virtualization for client-side datasets. `"auto"` (the
   * default) switches at 250 loaded rows; `false` retains paginated semantic
   * table markup; `true` always renders a virtualized grid. Server-paged
   * tables always retain server pagination, even if this is `true`.
   *
   * Virtualized tables render a
   * DIV-based ARIA grid that windows the *full* filtered+sorted row model
   * (pagination is disabled and the pagination footer is hidden); only the
   * rows in (and near) the viewport are mounted. Search/sort/faceted-filter/
   * selection still apply over the full row model.
   *
   */
  virtualized?: boolean | "auto";
  /**
   * Opt into server-driven pagination. `data` should hold only the current
   * page's rows; the table will not slice further. The caller owns the
   * pagination state and feeds it into its query params so each page is a
   * separate fetch. (Search/sort remain client-side over the loaded page —
   * pass `searchable={false}` if that's misleading for the dataset.)
   */
  serverSide?: {
    rowCount: number;
    pagination: PaginationState;
    onPaginationChange: (next: PaginationState) => void;
    /**
     * Controlled search for APIs that filter before pagination. When present,
     * the toolbar delegates to the caller and does not filter the current page
     * a second time.
     */
    search?: {
      value: string;
      onChange: (value: string) => void;
    };
  };
}

// ============================================================
// DataTable Component
// ============================================================

export function DataTable<T extends RowData>({
  data,
  columns,
  keyExtractor,
  density,
  searchable = true,
  searchPlaceholder = "Search...",
  selectable = false,
  onRowClick,
  onSelectionChange,
  bulkActions,
  pageSize = 20,
  loadingRows,
  emptyState = {
    title: "No records yet",
    description: "Records will appear here when they are available.",
  },
  filtersActive = false,
  onClearFilters,
  loading = false,
  isError = false,
  error,
  permission,
  errorMessage = "Failed to load — try again",
  onRetry,
  toolbar,
  className,
  persistKey,
  resizable = false,
  virtualized = "auto",
  serverSide,
}: DataTableProps<T>) {
  const {
    table,
    activeColumns,
    facetColumns,
    rows,
    selectedRows,
    filteredEmptyState,
    effectiveVirtualized,
    cellPadding,
    selectPadding,
    skeletonRows,
    scrollRef,
    estimateSize,
    rowVirtualizer,
    focusedRowIndex,
    setFocusedRowIndex,
    focusRowAt,
    page,
    effPageSize,
    totalPages,
    totalRows,
    searchInput,
    setSearchInput,
  } = useDataTableController({
    data,
    columns,
    keyExtractor,
    density,
    selectable,
    onSelectionChange,
    pageSize,
    loadingRows,
    emptyState,
    filtersActive,
    onClearFilters,
    persistKey,
    resizable,
    virtualized,
    serverSide,
  });

  return (
    <div className={cn("space-y-3", className)}>
      <DataTableToolbar
        table={table}
        columns={columns}
        facetColumns={facetColumns}
        searchable={searchable}
        searchPlaceholder={searchPlaceholder}
        searchInput={serverSide?.search?.value ?? searchInput}
        onSearchInputChange={serverSide?.search?.onChange ?? setSearchInput}
        toolbar={toolbar}
        selectable={selectable}
        selectedRows={selectedRows}
        bulkActions={bulkActions}
      />

      {/* Table — virtualized (DIV grid) branch */}
      {effectiveVirtualized ? (
        <VirtualizedGrid
          activeColumns={activeColumns}
          table={table}
          rows={rows}
          rowVirtualizer={rowVirtualizer}
          scrollRef={scrollRef}
          totalRows={rows.length}
          selectable={selectable}
          resizable={resizable}
          cellPadding={cellPadding}
          selectPadding={selectPadding}
          rowHeight={estimateSize}
          loading={loading}
          skeletonRows={skeletonRows}
          emptyState={filteredEmptyState}
          isError={isError}
          error={error}
          permission={permission}
          errorMessage={errorMessage}
          onRetry={onRetry}
          keyExtractor={keyExtractor}
          onRowClick={onRowClick}
          focusedRowIndex={focusedRowIndex}
          setFocusedRowIndex={setFocusedRowIndex}
          focusRowAt={focusRowAt}
        />
      ) : (
        /* Table — default (semantic table) branch */
        <SemanticDataTable
          table={table}
          activeColumns={activeColumns}
          selectable={selectable}
          resizable={resizable}
          cellPadding={cellPadding}
          selectPadding={selectPadding}
          loading={loading}
          skeletonRows={skeletonRows}
          emptyState={filteredEmptyState}
          isError={isError}
          error={error}
          permission={permission}
          errorMessage={errorMessage}
          onRetry={onRetry}
          keyExtractor={keyExtractor}
          onRowClick={onRowClick}
        />
      )}

      {!effectiveVirtualized && (
        <DataTablePagination
          table={table}
          page={page}
          pageSize={effPageSize}
          pageCount={totalPages}
          rowCount={totalRows}
          currentRowCount={rows.length}
        />
      )}
    </div>
  );
}
