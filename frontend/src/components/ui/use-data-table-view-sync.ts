import { useCallback, useMemo, useRef } from "react";
import type {
  ColumnFiltersState,
  ColumnVisibilityState,
  SortingState,
  Updater,
} from "@tanstack/react-table";

import type { Column, DataTableProps } from "@/components/ui/data-table";
import type { ColumnPinningState } from "@/components/ui/data-table-layout";
import {
  decodeViewState,
  encodeViewState,
  sanitizeViewState,
  VIEW_STATE_VERSION,
  viewParamName,
  type TableViewState,
} from "@/components/ui/data-table-view-state";

interface ViewSyncOptions<T> {
  columns: Column<T>[];
  columnKeys: Set<string>;
  persistKey: string | undefined;
  effectiveServerSide: DataTableProps<T>["serverSide"];
  searchInput: string;
  effectiveColumnFilters: ColumnFiltersState;
  sorting: SortingState;
  columnVisibility: ColumnVisibilityState;
  columnOrder: string[];
  userPinning: ColumnPinningState | null;
  table: { setPageIndex: (index: number) => void };
  state: {
    setSearchInput: (next: string) => void;
    setSorting: (next: Updater<SortingState>) => void;
    setColumnFilters: (next: Updater<ColumnFiltersState>) => void;
    setColumnVisibility: (next: ColumnVisibilityState) => void;
    persistVisibility: (next: ColumnVisibilityState) => void;
    setColumnOrder: (next: string[]) => void;
    setUserPinning: (next: ColumnPinningState | null) => void;
  };
}

/** Current/applied table view state, shared by the URL param and saved views. */
export function useDataTableViewSync<T>({
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
}: ViewSyncOptions<T>) {
  const defaultHidden = useMemo(
    () => columns.filter((c) => c.hidden).map((c) => c.key),
    [columns],
  );
  const searchValue = effectiveServerSide?.search?.value ?? searchInput;
  const sortingValue = effectiveServerSide?.sorting?.value ?? sorting;
  const currentViewState = useMemo<TableViewState>(() => {
    const state: TableViewState = { v: VIEW_STATE_VERSION };
    if (searchValue) state.search = searchValue;
    const filters = Object.fromEntries(
      effectiveColumnFilters
        .filter((f) => Array.isArray(f.value) && f.value.length > 0)
        .map((f) => [f.id, f.value as string[]]),
    );
    if (Object.keys(filters).length) state.filters = filters;
    if (sortingValue.length) {
      state.sort = sortingValue.map(({ id, desc }) => ({ id, desc }));
    }
    const hidden = columns
      .filter((c) => columnVisibility[c.key] === false)
      .map((c) => c.key);
    const sameHidden =
      hidden.length === defaultHidden.length &&
      hidden.every((k) => defaultHidden.includes(k));
    if (!sameHidden) state.hidden = hidden;
    if (columnOrder.length) state.order = columnOrder;
    if (userPinning) state.pinning = userPinning;
    return state;
  }, [
    searchValue,
    effectiveColumnFilters,
    sortingValue,
    columns,
    columnVisibility,
    defaultHidden,
    columnOrder,
    userPinning,
  ]);

  const applyViewState = useCallback(
    (raw: TableViewState) => {
      const state = sanitizeViewState(raw, columnKeys);
      const nextSearch = state.search ?? "";
      if (effectiveServerSide?.search) {
        effectiveServerSide.search.onChange(nextSearch);
      } else {
        setSearchInput(nextSearch);
      }
      const nextFilters: ColumnFiltersState = Object.entries(
        state.filters ?? {},
      ).map(([id, value]) => ({ id, value }));
      if (effectiveServerSide?.filtering) {
        effectiveServerSide.filtering.onChange(nextFilters);
      } else {
        setColumnFilters(nextFilters);
      }
      const nextSort = state.sort ?? [];
      if (effectiveServerSide?.sorting) {
        effectiveServerSide.sorting.onChange(nextSort);
      } else {
        setSorting(nextSort);
      }
      const hidden = new Set(state.hidden ?? defaultHidden);
      const nextVisibility: ColumnVisibilityState = {};
      for (const col of columns) {
        // Structural columns can never be hidden by a view.
        const structural =
          col.rowActions || col.hideable === false || col.header.trim() === "";
        nextVisibility[col.key] = structural || !hidden.has(col.key);
      }
      if (columns.some((c) => nextVisibility[c.key])) {
        setColumnVisibility(nextVisibility);
        persistVisibility(nextVisibility);
      }
      setColumnOrder(state.order ?? []);
      setUserPinning(state.pinning ?? null);
      table.setPageIndex(0);
    },
    [
      columnKeys,
      columns,
      defaultHidden,
      effectiveServerSide,
      persistVisibility,
      setColumnFilters,
      setColumnOrder,
      setColumnVisibility,
      setSearchInput,
      setSorting,
      setUserPinning,
      table,
    ],
  );

  const urlParam = persistKey ? viewParamName(persistKey) : undefined;
  const initialUrlState = useRef<TableViewState | null>(null);
  // Called once by the URL sync child when the table mounts.
  const onInitialUrl = useCallback(
    (value: string | null) => {
      initialUrlState.current = decodeViewState(value, columnKeys);
      if (initialUrlState.current) applyViewState(initialUrlState.current);
    },
    [columnKeys, applyViewState],
  );
  const encodedView = encodeViewState(currentViewState);

  return {
    currentViewState,
    applyViewState,
    urlParam,
    onInitialUrl,
    encodedView,
    hasInitialUrlState: () => initialUrlState.current !== null,
  };
}
