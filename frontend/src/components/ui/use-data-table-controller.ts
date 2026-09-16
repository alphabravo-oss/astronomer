import { useMemo, useRef, useState } from "react";
import {
  useTable,
  type ColumnDef,
  type ColumnSizingState,
  type ColumnVisibilityState,
  type PaginationState,
  type RowData,
  type RowSelectionState,
  type TableOptions,
  type Updater,
} from "@tanstack/react-table";

import type { Column, DataTableProps } from "@/components/ui/data-table";
import type { TableEmptyState } from "@/components/ui/data-table-empty-state";
import {
  dataTableFeatures,
  type DataTableFeatures,
} from "@/components/ui/data-table-features";
import { searchIndexMatches } from "@/components/ui/data-table-search";
import { sortValue } from "@/components/ui/data-table-state";
import { useDataTableState } from "@/components/ui/use-data-table-state";
import { useVirtualRows } from "@/components/ui/use-virtual-rows";
import { useUserPreferences } from "@/lib/user-preferences";

const CLIENT_SIDE_VIRTUALIZATION_THRESHOLD = 250;

interface DataTableControllerOptions<T extends RowData> {
  data: T[];
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  density: DataTableProps<T>["density"];
  selectable: NonNullable<DataTableProps<T>["selectable"]>;
  onSelectionChange: DataTableProps<T>["onSelectionChange"];
  pageSize: number;
  loadingRows: number | undefined;
  emptyState: TableEmptyState;
  filtersActive: boolean;
  onClearFilters: DataTableProps<T>["onClearFilters"];
  persistKey: string | undefined;
  resizable: boolean;
  virtualized: NonNullable<DataTableProps<T>["virtualized"]>;
  serverSide: DataTableProps<T>["serverSide"];
}

export function useDataTableController<T extends RowData>({
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
}: DataTableControllerOptions<T>) {
  const { preferences } = useUserPreferences();
  const effectiveDensity = density ?? preferences.table_density;
  // A server page is not the complete row model, so server pagination always
  // wins. This prevents an accidental `virtualized` prop from silently hiding
  // the rest of a server-paged collection.
  const effectiveServerSide = serverSide;
  const effectiveVirtualized =
    !effectiveServerSide &&
    (virtualized === true ||
      (virtualized === "auto" &&
        data.length >= CLIENT_SIDE_VIRTUALIZATION_THRESHOLD));
  const {
    searchInput,
    setSearchInput,
    globalFilter,
    clientPagination,
    setClientPagination,
    sorting,
    setSorting,
    columnFilters,
    setColumnFilters,
    rowSelection,
    setRowSelection,
    columnVisibility,
    setColumnVisibility,
    columnSizing,
    setColumnSizing,
    persistSizing,
    persistVisibility,
    filteredEmptyState,
    searchIndex,
  } = useDataTableState({
    data,
    columns,
    keyExtractor,
    pageSize,
    emptyState,
    filtersActive,
    onClearFilters,
    persistKey,
    resizable,
  });
  const cellPadding =
    effectiveDensity === "compact" ? "px-3 py-2" : "px-4 py-3";
  const selectPadding =
    effectiveDensity === "compact" ? "px-3 py-2" : "px-3 py-3";
  const skeletonRows = loadingRows ?? Math.min(pageSize, 8);

  const columnDefs = useMemo<ColumnDef<DataTableFeatures, T>[]>(
    () =>
      columns.map((col) => ({
        id: col.key,
        accessorFn: (row: T) => sortValue(col, row),
        enableSorting: col.sortable !== false,
        enableHiding: true,
        enableColumnFilter: !!col.filter,
        // Faceted multi-select: keep the row when nothing is selected, otherwise
        // when its (stringified) value is among the selected facet values.
        filterFn: (row, columnId, value) => {
          const selected = (value as string[]) ?? [];
          return (
            selected.length === 0 ||
            selected.includes(String(row.getValue(columnId)))
          );
        },
        // Numeric sortAccessors sort numerically; everything else by locale.
        // react-table negates this for descending, so we return the ascending
        // comparison — matching the old `aVal - bVal` / localeCompare logic.
        sortFn: (a, b, columnId) => {
          const av = a.getValue(columnId);
          const bv = b.getValue(columnId);
          if (typeof av === "number" && typeof bv === "number") {
            return av === bv ? 0 : av < bv ? -1 : 1;
          }
          return String(av).localeCompare(String(bv));
        },
      })),
    [columns],
  );

  const tableOptions = useMemo<TableOptions<DataTableFeatures, T>>(
    () => ({
      features: dataTableFeatures,
      data,
      columns: columnDefs,
      getRowId: keyExtractor,
      state: {
        globalFilter,
        sorting,
        columnFilters,
        rowSelection,
        columnVisibility,
        ...(resizable ? { columnSizing } : {}),
        pagination: effectiveServerSide?.pagination ?? clientPagination,
      },
      ...(resizable
        ? {
            enableColumnResizing: true,
            columnResizeMode: "onChange" as const,
            onColumnSizingChange: (updater: Updater<ColumnSizingState>) => {
              const next =
                typeof updater === "function" ? updater(columnSizing) : updater;
              setColumnSizing(next);
              persistSizing(next);
            },
          }
        : {}),
      manualPagination: !!effectiveServerSide || effectiveVirtualized,
      onPaginationChange: setClientPagination,
      ...(effectiveServerSide
        ? {
            rowCount: effectiveServerSide.rowCount,
            onPaginationChange: (updater: Updater<PaginationState>) => {
              const next =
                typeof updater === "function"
                  ? updater(effectiveServerSide.pagination)
                  : updater;
              effectiveServerSide.onPaginationChange(next);
            },
          }
        : {}),
      enableRowSelection:
        typeof selectable === "function"
          ? (row) => selectable(row.original)
          : selectable,
      enableSortingRemoval: false, // 2-state toggle (asc ⇄ desc), never back to unsorted
      sortDescFirst: false, // always start ascending, even for numeric columns
      // The old hand-rolled table only reset to page 1 on a *search* change (done
      // explicitly in the input handler) — never on a data refetch. Default
      // autoReset would snap polling tables back to page 1 on every poll, so disable it.
      autoResetPageIndex: false,
      // Global search over cached visible-cell haystacks. No column accessor is
      // evaluated on the filter path.
      globalFilterFn: (row, _columnId, filterValue) =>
        searchIndexMatches(searchIndex, row.id, filterValue),
      // Programmatic setGlobalFilter routes through the same debounce as typing.
      onGlobalFilterChange: (updater: Updater<string>) =>
        setSearchInput((prev) =>
          typeof updater === "function" ? updater(prev) : updater,
        ),
      onSortingChange: setSorting,
      onColumnFiltersChange: setColumnFilters,
      onColumnVisibilityChange: (updater: Updater<ColumnVisibilityState>) => {
        const next =
          typeof updater === "function" ? updater(columnVisibility) : updater;
        // Never allow hiding the last visible column.
        const visibleCount = columns.filter(
          (c) => next[c.key] !== false,
        ).length;
        if (visibleCount < 1) return;
        setColumnVisibility(next);
        persistVisibility(next);
      },
      onRowSelectionChange: (updater: Updater<RowSelectionState>) => {
        const next =
          typeof updater === "function" ? updater(rowSelection) : updater;
        setRowSelection(next);
        onSelectionChange?.(data.filter((row) => next[keyExtractor(row)]));
      },
      initialState: { pagination: { pageIndex: 0, pageSize } },
    }),
    [
      data,
      columnDefs,
      keyExtractor,
      globalFilter,
      sorting,
      columnFilters,
      rowSelection,
      columnVisibility,
      resizable,
      columnSizing,
      effectiveServerSide,
      persistSizing,
      persistVisibility,
      setColumnFilters,
      setColumnSizing,
      setColumnVisibility,
      setRowSelection,
      setSearchInput,
      setSorting,
      selectable,
      searchIndex,
      columns,
      onSelectionChange,
      effectiveVirtualized,
      pageSize,
      clientPagination,
      setClientPagination,
    ],
  );
  const table = useTable(tableOptions);

  // A column is visible unless explicitly toggled off. Derived from the
  // columnVisibility state (which we own) rather than querying the table, so the
  // memo deps are exactly what it reads.
  const activeColumns = useMemo(
    () => columns.filter((c) => columnVisibility[c.key] !== false),
    [columns, columnVisibility],
  );

  // Faceted filters render for visible columns that opted in via `filter`.
  const facetColumns = activeColumns.filter((c) => c.filter);

  const rows = table.getRowModel().rows;
  const selectedRows = table.getSelectedRowModel().rows.map((r) => r.original);
  const filteredCount = table.getFilteredRowModel().rows.length;
  const totalPages = table.getPageCount();
  const page = table.state.pagination.pageIndex;
  // Footer counts: server mode reports the server total; client mode the
  // filtered-row count. `effPageSize` is the page size actually in effect.
  const effPageSize = effectiveServerSide
    ? effectiveServerSide.pagination.pageSize
    : pageSize;
  const totalRows = effectiveServerSide
    ? effectiveServerSide.rowCount
    : filteredCount;

  // ---- Virtualization ----
  // The scroll container that the virtualizer measures against. Only used by
  // the virtualized render branch, but the hook must run unconditionally (rules
  // of hooks), so it is always created — it is cheap when `virtualized` is off.
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const estimateSize = effectiveDensity === "compact" ? 40 : 52;
  const rowVirtualizer = useVirtualRows({
    count: rows.length,
    scrollRef,
    estimateSize,
    overscan: 12,
    enabled: effectiveVirtualized,
  });
  // Roving-tabindex focus index for the virtualized grid body. -1 = none yet.
  const [focusedRowIndex, setFocusedRowIndex] = useState(-1);

  // Move keyboard focus between virtual rows. Because off-screen rows are not
  // mounted, we ask the virtualizer to scroll the target into view first, then
  // focus it on the next frame once it has been rendered.
  const focusRowAt = (target: number) => {
    if (target < 0 || target >= rows.length) return;
    setFocusedRowIndex(target);
    rowVirtualizer.scrollToIndex(target, { align: "auto" });
    requestAnimationFrame(() => {
      const el =
        scrollRef.current?.querySelector<HTMLElement>(
          `[data-row-index="${target}"]`,
        ) ?? null;
      el?.focus();
    });
  };

  return {
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
  };
}
