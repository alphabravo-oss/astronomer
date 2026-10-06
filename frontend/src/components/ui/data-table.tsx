import { useContext, useMemo, useRef } from "react";
import { QueryClientContext } from "@tanstack/react-query";
import type { RowData } from "@tanstack/react-table";
import { orderColumns, reorderWithin } from "@/components/ui/data-table-layout";
import { columnFullName } from "@/components/ui/data-table-header-label";
import { downloadCsv, toCsv } from "@/components/ui/data-table-csv";
import {
  buildFilterChips,
  DataTableFilterChips,
  removeFilterValue,
} from "@/components/ui/data-table-filter-chips";
import { DataTableUrlSync } from "@/components/ui/data-table-url-sync";
import { DataTableViewsMenu } from "@/components/ui/data-table-views-menu";
import { viewTableKey } from "@/components/ui/data-table-view-state";
import { useAuthStore } from "@/lib/store";
import { cn } from "@/lib/utils";

import { DataTableToolbar } from "@/components/ui/data-table-toolbar";
import { DataTablePagination } from "@/components/ui/data-table-pagination";
import { VirtualizedGrid } from "@/components/ui/data-table-virtualized-body";
import { SemanticDataTable } from "@/components/ui/data-table-semantic-view";
import { useDataTableController } from "@/components/ui/use-data-table-controller";
import { useTableKeyboardNav } from "@/components/ui/use-table-keyboard-nav";
import type { Column, DataTableProps } from "@/components/ui/data-table-types";

export type { Column, DataTableProps };

// ============================================================
// DataTable Component
// ============================================================

export function DataTable<T extends RowData>({
  groupBy,
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
  renderSubRow,
  exportCsv = false,
  savedViews,
  keyboardNav = true,
  className,
  persistKey,
  resizable = false,
  virtualized = "auto",
  layout = "fit",
  serverSide,
}: DataTableProps<T>) {
  const {
    table,
    activeColumns,
    layouts,
    placements,
    defaultPin,
    expandable,
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
    columnOrder,
    setColumnOrder,
    setUserPinning,
    fullPinning,
    currentViewState,
    applyViewState,
    hasInitialUrlState,
    urlParam,
    encodedView,
    onInitialUrl,
    exportRows,
    isServerSide,
  } = useDataTableController({
    groupBy,
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
    layout,
    renderSubRow,
  });

  const searchRef = useRef<HTMLInputElement>(null);
  const wrapperRef = useRef<HTMLDivElement>(null);
  const searchValue = serverSide?.search?.value ?? searchInput;
  const setSearch = serverSide?.search?.onChange ?? setSearchInput;

  // ---- Columns menu: order and pinning ----
  const menuColumns = useMemo(
    () => orderColumns(columns, columnOrder, fullPinning),
    [columns, columnOrder, fullPinning],
  );
  const movable = useMemo(
    () =>
      new Set(
        columns
          .filter(
            (c) =>
              !c.rowActions && c.hideable !== false && c.header.trim() !== "",
          )
          .map((c) => c.key),
      ),
    [columns],
  );
  const moveColumnKey = (key: string, direction: -1 | 1) =>
    setColumnOrder(
      reorderWithin(
        menuColumns.map((c) => c.key),
        movable,
        key,
        direction,
      ),
    );
  const pinColumn = (key: string, side: "start" | "end" | false) =>
    setUserPinning({
      start: [
        ...fullPinning.start.filter((k) => k !== key),
        ...(side === "start" ? [key] : []),
      ],
      end: [
        ...fullPinning.end.filter((k) => k !== key),
        ...(side === "end" ? [key] : []),
      ],
    });
  const resetColumns = () => {
    setColumnOrder([]);
    setUserPinning(null);
  };
  void defaultPin;

  // ---- Filter chips ----
  const columnFilters = table.state.columnFilters;
  const chips = useMemo(
    () =>
      buildFilterChips(
        columnFilters,
        (id) => {
          const column = columns.find((c) => c.key === id);
          return (
            column?.filter?.label ?? (column ? columnFullName(column) : id)
          );
        },
        searchValue,
      ),
    [columnFilters, columns, searchValue],
  );
  // The chip row is for faceted filters; a lone search term already shows
  // in the search box.
  const hasFacetChips = chips.some((chip) => chip.id !== "search");

  // ---- CSV ----
  const csvOptions = typeof exportCsv === "object" ? exportCsv : undefined;
  const exportEnabled = exportCsv !== false;
  const runExport = () => {
    const exportColumns = activeColumns.filter(
      (c) => !c.rowActions && c.header.trim() !== "",
    );
    const base = (csvOptions?.filename ?? persistKey ?? "table").replace(
      /[^a-zA-Z0-9._-]+/g,
      "-",
    );
    const stamp = new Date().toISOString().slice(0, 10);
    downloadCsv(`${base}-${stamp}.csv`, toCsv(exportColumns, exportRows()));
  };

  // ---- Saved views ----
  const queryClient = useContext(QueryClientContext);
  const isAuthenticated = useAuthStore((state) => state.isAuthenticated);
  const columnKeys = useMemo(
    () => new Set(columns.map((c) => c.key)),
    [columns],
  );
  const viewsMenu =
    persistKey && savedViews !== false && queryClient && isAuthenticated ? (
      <DataTableViewsMenu
        tableKey={viewTableKey(persistKey)}
        columnKeys={columnKeys}
        current={currentViewState}
        onApply={applyViewState}
        hasInitialUrlState={hasInitialUrlState}
      />
    ) : undefined;

  // ---- Keyboard row navigation ----
  const onKeyDown = useTableKeyboardNav({
    keyboardNav,
    searchable,
    selectable,
    searchRef,
    wrapperRef,
    rows,
    effectiveVirtualized,
    focusRowAt,
  });

  return (
    // eslint-disable-next-line jsx-a11y/no-static-element-interactions -- key events bubble from focusable rows.
    <div
      ref={wrapperRef}
      className={cn("space-y-3", className)}
      onKeyDown={onKeyDown}
    >
      {urlParam && (
        <DataTableUrlSync
          param={urlParam}
          encoded={encodedView}
          onInitial={onInitialUrl}
        />
      )}
      <DataTableToolbar
        table={table}
        columns={columns}
        facetColumns={facetColumns}
        searchable={searchable}
        searchPlaceholder={searchPlaceholder}
        searchInput={searchValue}
        onSearchInputChange={setSearch}
        searchInputRef={searchRef}
        toolbar={toolbar}
        selectable={selectable}
        selectedRows={selectedRows}
        bulkActions={bulkActions}
        menuColumns={menuColumns}
        pinning={fullPinning}
        onMoveColumn={moveColumnKey}
        onPinColumn={pinColumn}
        onResetColumns={resetColumns}
        onExportCsv={exportEnabled ? runExport : undefined}
        exportPageOnly={isServerSide}
        viewsMenu={viewsMenu}
      />

      {hasFacetChips && (
        <DataTableFilterChips
          chips={chips}
          onRemove={(chip) => {
            if (chip.id === "search") {
              setSearch("");
              return;
            }
            const sep = chip.id.indexOf("=");
            table.setColumnFilters(
              removeFilterValue(
                columnFilters,
                chip.id.slice(0, sep),
                chip.value,
              ),
            );
            table.setPageIndex(0);
          }}
          onClearAll={() => {
            table.setColumnFilters([]);
            setSearch("");
            table.setPageIndex(0);
          }}
        />
      )}

      {/* Table — virtualized (DIV grid) branch */}
      {effectiveVirtualized ? (
        <VirtualizedGrid
          groupBy={groupBy}
          activeColumns={activeColumns}
          layouts={layouts}
          placements={placements}
          expandable={expandable}
          renderSubRow={renderSubRow}
          table={table}
          rows={rows}
          rowVirtualizer={rowVirtualizer}
          scrollRef={scrollRef}
          totalRows={rows.length}
          selectable={selectable}
          resizable={resizable}
          layout={layout}
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
          rows={rows}
          groupBy={groupBy}
          table={table}
          activeColumns={activeColumns}
          layouts={layouts}
          placements={placements}
          expandable={expandable}
          renderSubRow={renderSubRow}
          keyboardNav={keyboardNav}
          selectable={selectable}
          resizable={resizable}
          layout={layout}
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
          rowCountIsLowerBound={serverSide?.rowCountIsLowerBound}
          countUnavailable={loading || isError || serverSide?.rowCountIsUnknown}
          currentRowCount={rows.length}
        />
      )}
    </div>
  );
}
