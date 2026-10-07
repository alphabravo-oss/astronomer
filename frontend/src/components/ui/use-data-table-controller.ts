import { useMemo, useRef, useState } from "react";
import {
  useTable,
  type ColumnDef,
  type ColumnFiltersState,
  type ColumnSizingState,
  type ColumnVisibilityState,
  type ExpandedState,
  type PaginationState,
  type RowData,
  type RowSelectionState,
  type SortingState,
  type TableOptions,
  type Updater,
} from "@tanstack/react-table";

import type { Column, DataTableProps } from "@/components/ui/data-table";
import type { TableEmptyState } from "@/components/ui/data-table-empty-state";
import {
  dataTableFeatures,
  type DataTableFeatures,
} from "@/components/ui/data-table-features";
import {
  defaultPinning,
  normalizePinning,
  orderColumns,
  pinnedPlacements,
  resolveColumnLayout,
  SELECT_COLUMN_WIDTH,
  type ColumnPinningState,
} from "@/components/ui/data-table-layout";
import { searchIndexMatches } from "@/components/ui/data-table-search";
import {
  sortValue,
  serverNavigationRowCount,
} from "@/components/ui/data-table-state";
import { useDataTableState } from "@/components/ui/use-data-table-state";
import { useVirtualRows } from "@/components/ui/use-virtual-rows";
import { useDataTableViewSync } from "@/components/ui/use-data-table-view-sync";
import { useUserPreferences } from "@/lib/user-preferences";
import { groupTableRows } from "./data-table-grouping";

const CLIENT_SIDE_VIRTUALIZATION_THRESHOLD = 250;

interface DataTableControllerOptions<T extends RowData> {
  groupBy?: (row: T) => string;
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
  layout?: "fit" | "scroll";
  renderSubRow?: DataTableProps<T>["renderSubRow"];
}

const EMPTY_ORDER: string[] = [];

export function useDataTableController<T extends RowData>({
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
  layout = "fit",
  renderSubRow,
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
    columnOrder,
    setColumnOrder,
    userPinning,
    setUserPinning,
    expanded,
    setExpanded,
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
  // `--row-py` (set by the density tokens) wins when present; the fallback
  // keeps the table correct where the token is not defined. An explicit
  // `density` prop pins the padding regardless of the token.
  const compact = effectiveDensity === "compact";
  const rowPy =
    density !== undefined
      ? compact
        ? "py-2"
        : "py-3"
      : compact
        ? "py-[var(--row-py,0.5rem)]"
        : "py-[var(--row-py,0.75rem)]";
  const cellPadding = `${compact ? "px-3" : "px-4"} ${rowPy}`;
  const selectPadding = `px-3 ${rowPy}`;
  const skeletonRows = loadingRows ?? Math.min(pageSize, 8);
  const expandable = !!renderSubRow;

  const columnDefs = useMemo<ColumnDef<DataTableFeatures, T>[]>(
    () =>
      columns.map((col) => ({
        id: col.key,
        accessorFn: (row: T) => sortValue(col, row),
        enableSorting: !col.rowActions && col.sortable !== false,
        enableResizing: !col.rowActions && col.header.trim() !== "",
        enableHiding:
          !col.rowActions && col.hideable !== false && col.header.trim() !== "",
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

  const columnKeys = useMemo(
    () => new Set(columns.map((c) => c.key)),
    [columns],
  );
  const defaultPin = useMemo(
    () => defaultPinning(columns, layout),
    [columns, layout],
  );
  const pinning = useMemo(
    () => normalizePinning(userPinning ?? defaultPin, columnKeys),
    [userPinning, defaultPin, columnKeys],
  );
  const effectiveColumnFilters =
    effectiveServerSide?.filtering?.value ?? columnFilters;

  const tableOptions = useMemo<TableOptions<DataTableFeatures, T>>(
    () => ({
      features: dataTableFeatures,
      data,
      columns: columnDefs,
      getRowId: keyExtractor,
      state: {
        globalFilter,
        sorting: effectiveServerSide?.sorting?.value ?? sorting,
        columnFilters: effectiveColumnFilters,
        rowSelection,
        columnVisibility,
        columnOrder,
        columnPinning: pinning,
        expanded,
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
      manualSorting: !!effectiveServerSide?.sorting,
      manualFiltering: !!effectiveServerSide?.filtering,
      onPaginationChange: setClientPagination,
      ...(effectiveServerSide
        ? {
            rowCount: serverNavigationRowCount(effectiveServerSide),
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
      getRowCanExpand: () => expandable,
      // Polling refreshes must not collapse rows the user opened.
      autoResetExpanded: false,
      onExpandedChange: (updater: Updater<ExpandedState>) =>
        setExpanded(updater),
      onColumnOrderChange: (updater: Updater<string[]>) =>
        setColumnOrder(
          typeof updater === "function" ? updater(columnOrder) : updater,
        ),
      onColumnPinningChange: (updater: Updater<ColumnPinningState>) =>
        setUserPinning(
          typeof updater === "function" ? updater(pinning) : updater,
        ),
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
      onSortingChange: (updater: Updater<SortingState>) => {
        if (effectiveServerSide?.sorting) {
          const current = effectiveServerSide.sorting.value;
          const next =
            typeof updater === "function" ? updater(current) : updater;
          effectiveServerSide.sorting.onChange(next);
          return;
        }
        setSorting(updater);
      },
      onColumnFiltersChange: (updater: Updater<ColumnFiltersState>) => {
        if (effectiveServerSide?.filtering) {
          const current = effectiveServerSide.filtering.value;
          effectiveServerSide.filtering.onChange(
            typeof updater === "function" ? updater(current) : updater,
          );
          return;
        }
        setColumnFilters(updater);
      },
      onColumnVisibilityChange: (updater: Updater<ColumnVisibilityState>) => {
        const next =
          typeof updater === "function" ? updater(columnVisibility) : updater;
        // Never allow hiding the last visible column.
        const visibleCount = columns.filter(
          (c) =>
            !c.rowActions &&
            c.hideable !== false &&
            c.header.trim() !== "" &&
            next[c.key] !== false,
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
      effectiveColumnFilters,
      rowSelection,
      columnVisibility,
      columnOrder,
      pinning,
      expanded,
      expandable,
      resizable,
      columnSizing,
      effectiveServerSide,
      persistSizing,
      persistVisibility,
      setColumnFilters,
      setColumnOrder,
      setUserPinning,
      setExpanded,
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
  // memo deps are exactly what it reads. Display order is start-pinned, the
  // user's order, then end-pinned.
  const activeColumns = useMemo(
    () =>
      orderColumns(
        columns.filter((c) => columnVisibility[c.key] !== false),
        columnOrder.length ? columnOrder : EMPTY_ORDER,
        pinning,
      ),
    [columns, columnVisibility, columnOrder, pinning],
  );
  const leadWidth =
    (selectable ? SELECT_COLUMN_WIDTH : 0) + (expandable ? 40 : 0);
  const activePinning = useMemo(
    () => normalizePinning(pinning, new Set(activeColumns.map((c) => c.key))),
    [pinning, activeColumns],
  );
  const placements = useMemo(
    () => pinnedPlacements(activeColumns, activePinning, leadWidth),
    [activeColumns, activePinning, leadWidth],
  );
  const layouts = useMemo(
    () => new Map(activeColumns.map((c) => [c.key, resolveColumnLayout(c)])),
    [activeColumns],
  );

  // Faceted filters render for visible columns that opted in via `filter`.
  const facetColumns = activeColumns.filter((c) => c.filter);

  const rows = groupTableRows(
    table.getRowModel().rows,
    groupBy ? (row) => groupBy(row.original) : undefined,
  );
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

  // ---- View state (URL + saved views) ----
  const {
    currentViewState,
    applyViewState,
    urlParam,
    onInitialUrl,
    encodedView,
    hasInitialUrlState,
  } = useDataTableViewSync({
    columns,
    columnKeys,
    persistKey,
    effectiveServerSide,
    searchInput,
    effectiveColumnFilters,
    sorting,
    columnVisibility,
    columnOrder,
    userPinning,
    table,
    state: {
      setSearchInput,
      setSorting,
      setColumnFilters,
      setColumnVisibility,
      persistVisibility,
      setColumnOrder,
      setUserPinning,
    },
  });

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

  // Rows for CSV export: the filtered and sorted model across every client
  // page. A server-paged table only holds its current page.
  const exportRows = () =>
    (effectiveServerSide
      ? table.getRowModel().rows
      : table.getSortedRowModel().rows
    ).map((r) => r.original);

  return {
    table,
    activeColumns,
    layouts,
    placements,
    pinning: activePinning,
    fullPinning: pinning,
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
    currentViewState,
    applyViewState,
    hasInitialUrlState,
    urlParam,
    encodedView,
    onInitialUrl,
    exportRows,
    isServerSide: !!effectiveServerSide,
  };
}
