import type {
  ColumnSizingState,
  ColumnVisibilityState,
} from "@tanstack/react-table";

export function serverNavigationRowCount(server: {
  rowCount: number;
  rowCountIsLowerBound?: boolean;
  pagination: { pageIndex: number; pageSize: number };
}): number {
  // A short page can still advertise continuation. Do not turn its lower
  // bound into a disabled Next button, or display this navigation sentinel.
  return server.rowCountIsLowerBound
    ? Math.max(
        server.rowCount,
        (server.pagination.pageIndex + 1) * server.pagination.pageSize + 1,
      )
    : server.rowCount;
}

export interface SortableColumn<T> {
  accessor: (row: T) => unknown;
  sortAccessor?: (row: T) => string | number;
}

export const visibilityStorageKey = (persistKey: string) =>
  `dt:${persistKey}:visibility`;
export const sizingStorageKey = (persistKey: string) =>
  `dt:${persistKey}:sizing`;

export function parsePersistedVisibility(
  raw: string | null,
): ColumnVisibilityState {
  try {
    const value: unknown = raw ? JSON.parse(raw) : null;
    return value && typeof value === "object" && !Array.isArray(value)
      ? Object.fromEntries(
          Object.entries(value).filter(
            ([, entry]) => typeof entry === "boolean",
          ),
        )
      : {};
  } catch {
    return {};
  }
}

export function parsePersistedSizing(raw: string | null): ColumnSizingState {
  try {
    const value: unknown = raw ? JSON.parse(raw) : null;
    return value && typeof value === "object" && !Array.isArray(value)
      ? Object.fromEntries(
          Object.entries(value).filter(
            ([, entry]) =>
              typeof entry === "number" && Number.isFinite(entry) && entry > 0,
          ),
        )
      : {};
  } catch {
    return {};
  }
}

// Sort values intentionally match the table's historic cell-value fallback.
export function sortValue<T>(col: SortableColumn<T>, row: T): string | number {
  if (col.sortAccessor) return col.sortAccessor(row);
  const value = col.accessor(row);
  return value?.toString() ?? "";
}
