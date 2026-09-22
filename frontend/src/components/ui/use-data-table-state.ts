import { useMemo, useState } from "react";
import { useDebouncedValue } from "@tanstack/react-pacer";
import type {
  ColumnFiltersState,
  ColumnSizingState,
  PaginationState,
  RowSelectionState,
  SortingState,
} from "@tanstack/react-table";
import type { Column } from "@/components/ui/data-table";
import {
  resolveTableEmptyState,
  type TableEmptyState,
} from "@/components/ui/data-table-empty-state";
import { buildSearchIndex } from "@/components/ui/data-table-search";
import {
  parsePersistedSizing,
  parsePersistedVisibility,
  sizingStorageKey,
  visibilityStorageKey,
} from "@/components/ui/data-table-state";
import { useDraft } from "@/lib/hooks/use-draft";
import { useStorageSnapshot } from "@/lib/hooks/use-storage-snapshot";
import { useUserPreferences } from "@/lib/user-preferences";

interface DataTableStateOptions<T> {
  data: T[];
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  pageSize: number;
  emptyState: TableEmptyState;
  filtersActive: boolean;
  onClearFilters?: () => void;
  persistKey?: string;
  resizable: boolean;
}

export function useDataTableState<T>({
  data,
  columns,
  keyExtractor,
  pageSize,
  emptyState,
  filtersActive,
  onClearFilters,
  persistKey,
  resizable,
}: DataTableStateOptions<T>) {
  const [searchInput, setSearchInput] = useState("");
  const [globalFilter] = useDebouncedValue(searchInput, { wait: 200 });
  // The caller's `pageSize` is the fallback default (it's often tuned to a
  // server page limit, e.g. a resource table's server-side page). Only a
  // real, server-owned preference overrides it — `isServerOwned` is false
  // both before the initial load resolves and when signed out, and
  // defaultUserPreferences always carries a concrete rows_per_page that
  // would otherwise clobber every caller's tuned pageSize.
  const { preferences, isServerOwned } = useUserPreferences();
  const initialPageSize =
    (isServerOwned ? preferences.rows_per_page : undefined) ?? pageSize;
  const defaultPagination = useMemo(
    () => ({ pageIndex: 0, pageSize: initialPageSize }),
    [initialPageSize],
  );
  const [clientPagination, setClientPagination] = useDraft<PaginationState>(
    defaultPagination,
    `${globalFilter}:${initialPageSize}`,
  );
  const [sorting, setSorting] = useState<SortingState>([]);
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const [storedVisibility, persistVisibility] = useStorageSnapshot(
    persistKey ? visibilityStorageKey(persistKey) : undefined,
  );
  const [storedSizing, persistSizing] = useStorageSnapshot(
    persistKey && resizable ? sizingStorageKey(persistKey) : undefined,
  );
  const visibilitySource = useMemo(
    () => ({
      ...Object.fromEntries(
        columns
          .filter((column) => column.hidden)
          .map((column) => [column.key, false]),
      ),
      ...parsePersistedVisibility(storedVisibility),
      // Structural columns (normally the unlabeled row-actions column) stay
      // pinned even if an older browser preference hid them.
      ...Object.fromEntries(
        columns
          .filter(
            (column) =>
              column.hideable === false || column.header.trim() === "",
          )
          .map((column) => [column.key, true]),
      ),
    }),
    [columns, storedVisibility],
  );
  const sizingSource = useMemo(
    () => parsePersistedSizing(storedSizing),
    [storedSizing],
  );
  const [columnVisibility, setColumnVisibility] = useDraft(
    visibilitySource,
    `${persistKey ?? ""}:${storedVisibility ?? ""}`,
  );
  const [columnSizing, setColumnSizing] = useDraft<ColumnSizingState>(
    sizingSource,
    `${persistKey ?? ""}:${storedSizing ?? ""}:${resizable}`,
  );
  const filteredEmptyState = resolveTableEmptyState(
    emptyState,
    filtersActive || globalFilter.trim() !== "" || columnFilters.length > 0,
    !filtersActive || onClearFilters
      ? () => {
          setSearchInput("");
          setColumnFilters([]);
          onClearFilters?.();
        }
      : undefined,
  );
  const searchIndex = useMemo(
    () =>
      buildSearchIndex(
        data,
        columns,
        (column) => columnVisibility[column.key] !== false,
        keyExtractor,
      ),
    [columnVisibility, columns, data, keyExtractor],
  );

  return {
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
  };
}
