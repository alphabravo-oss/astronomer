'use client';

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import type { ReactNode } from 'react';
import { useEffect, useMemo, useState } from 'react';
import {
  useReactTable,
  getCoreRowModel,
  getFilteredRowModel,
  getSortedRowModel,
  getPaginationRowModel,
  type ColumnDef,
  type SortingState,
  type RowSelectionState,
  type VisibilityState,
  type Updater,
} from '@tanstack/react-table';
import {
  ChevronDown,
  ChevronUp,
  ChevronsUpDown,
  ChevronLeft,
  ChevronRight,
  Search,
  SlidersHorizontal,
  X,
} from 'lucide-react';
import { cn } from '@/lib/utils';

// ============================================================
// Types
// ============================================================

export interface Column<T> {
  key: string;
  header: string;
  accessor: (row: T) => React.ReactNode;
  sortAccessor?: (row: T) => string | number;
  sortable?: boolean;
  filterable?: boolean;
  hidden?: boolean;
  width?: string;
  align?: 'left' | 'center' | 'right';
}

interface DataTableProps<T> {
  data: T[];
  columns: Column<T>[];
  keyExtractor: (row: T) => string;
  density?: 'compact' | 'comfortable';
  searchable?: boolean;
  searchPlaceholder?: string;
  selectable?: boolean;
  onRowClick?: (row: T) => void;
  onSelectionChange?: (selected: T[]) => void;
  bulkActions?: (selected: T[]) => ReactNode;
  pageSize?: number;
  loadingRows?: number;
  emptyMessage?: string;
  loading?: boolean;
  toolbar?: ReactNode;
  className?: string;
  /**
   * When set, the user's column-visibility choices are persisted to
   * localStorage under this key and restored on next mount. Omit for
   * ephemeral (non-persisted) tables.
   */
  persistKey?: string;
}

const visibilityStorageKey = (persistKey: string) => `dt:${persistKey}:visibility`;

function readPersistedVisibility(persistKey: string | undefined): VisibilityState | null {
  if (!persistKey || typeof window === 'undefined') return null;
  try {
    const raw = window.localStorage.getItem(visibilityStorageKey(persistKey));
    return raw ? (JSON.parse(raw) as VisibilityState) : null;
  } catch {
    return null;
  }
}

// ============================================================
// Internals
// ============================================================

// Sort value for a column: prefer sortAccessor, else the stringified cell
// output (matching the previous hand-rolled behavior, where columns without a
// sortAccessor sorted on `accessor(row)?.toString()`).
function sortValue<T>(col: Column<T>, row: T): string | number {
  if (col.sortAccessor) return col.sortAccessor(row);
  const val = col.accessor(row);
  return val?.toString() ?? '';
}

// ============================================================
// DataTable Component
// ============================================================

export function DataTable<T>({
  data,
  columns,
  keyExtractor,
  density = 'comfortable',
  searchable = true,
  searchPlaceholder = 'Search...',
  selectable = false,
  onRowClick,
  onSelectionChange,
  bulkActions,
  pageSize = 20,
  loadingRows,
  emptyMessage = 'No results found',
  loading = false,
  toolbar,
  className,
  persistKey,
}: DataTableProps<T>) {
  const [globalFilter, setGlobalFilter] = useState('');
  const [sorting, setSorting] = useState<SortingState>([]);
  const [rowSelection, setRowSelection] = useState<RowSelectionState>({});
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>(() =>
    Object.fromEntries(columns.filter((c) => c.hidden).map((c) => [c.key, false]))
  );

  // Restore persisted visibility after mount. Reading localStorage during the
  // initial render would diverge from the server-rendered markup and trigger a
  // hydration mismatch, so we apply it in an effect instead.
  useEffect(() => {
    const stored = readPersistedVisibility(persistKey);
    if (stored) setColumnVisibility((prev) => ({ ...prev, ...stored }));
  }, [persistKey]);
  const [showColumnToggle, setShowColumnToggle] = useState(false);

  const cellPadding = density === 'compact' ? 'px-3 py-2' : 'px-4 py-3';
  const selectPadding = density === 'compact' ? 'px-3 py-2' : 'px-3 py-3';
  const skeletonRows = loadingRows ?? Math.min(pageSize, 8);

  // Lookup of original Column by key — used by sorting/global-filter without
  // threading metadata through react-table's typed column meta.
  const colByKey = useMemo(() => new Map(columns.map((c) => [c.key, c])), [columns]);

  const columnDefs = useMemo<ColumnDef<T>[]>(
    () =>
      columns.map((col) => ({
        id: col.key,
        accessorFn: (row: T) => sortValue(col, row),
        enableSorting: col.sortable !== false,
        enableHiding: true,
        // Numeric sortAccessors sort numerically; everything else by locale.
        // react-table negates this for descending, so we return the ascending
        // comparison — matching the old `aVal - bVal` / localeCompare logic.
        sortingFn: (a, b, columnId) => {
          const av = a.getValue(columnId);
          const bv = b.getValue(columnId);
          if (typeof av === 'number' && typeof bv === 'number') {
            return av === bv ? 0 : av < bv ? -1 : 1;
          }
          return String(av).localeCompare(String(bv));
        },
      })),
    [columns]
  );

  const table = useReactTable<T>({
    data,
    columns: columnDefs,
    getRowId: keyExtractor,
    state: { globalFilter, sorting, rowSelection, columnVisibility },
    enableRowSelection: selectable,
    enableSortingRemoval: false, // 2-state toggle (asc ⇄ desc), never back to unsorted
    sortDescFirst: false, // always start ascending, even for numeric columns
    // The old hand-rolled table only reset to page 1 on a *search* change (done
    // explicitly in the input handler) — never on a data refetch. Default
    // autoReset would snap polling tables back to page 1 on every poll, so disable it.
    autoResetPageIndex: false,
    // Global search: match the previous behavior — any *visible* column whose
    // stringified accessor output contains the query. The fn ignores columnId
    // and checks all visible cells, so a single match includes the row.
    globalFilterFn: (row, _columnId, filterValue) => {
      const q = String(filterValue ?? '').toLowerCase().trim();
      if (!q) return true;
      return row.getVisibleCells().some((cell) => {
        const original = colByKey.get(cell.column.id);
        if (!original) return false;
        const val = original.accessor(row.original);
        return val != null && String(val).toLowerCase().includes(q);
      });
    },
    onGlobalFilterChange: setGlobalFilter,
    onSortingChange: setSorting,
    onColumnVisibilityChange: (updater: Updater<VisibilityState>) =>
      setColumnVisibility((prev) => {
        const next = typeof updater === 'function' ? updater(prev) : updater;
        // Never allow hiding the last visible column.
        const visibleCount = columns.filter((c) => next[c.key] !== false).length;
        if (visibleCount < 1) return prev;
        if (persistKey && typeof window !== 'undefined') {
          try {
            window.localStorage.setItem(visibilityStorageKey(persistKey), JSON.stringify(next));
          } catch {
            /* ignore quota/availability errors — persistence is best-effort */
          }
        }
        return next;
      }),
    onRowSelectionChange: (updater: Updater<RowSelectionState>) =>
      setRowSelection((prev) => {
        const next = typeof updater === 'function' ? updater(prev) : updater;
        onSelectionChange?.(data.filter((row) => next[keyExtractor(row)]));
        return next;
      }),
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize } },
  });

  // A column is visible unless explicitly toggled off. Derived from the
  // columnVisibility state (which we own) rather than querying the table, so the
  // memo deps are exactly what it reads.
  const activeColumns = useMemo(
    () => columns.filter((c) => columnVisibility[c.key] !== false),
    [columns, columnVisibility]
  );

  const rows = table.getRowModel().rows;
  const selectedRows = table.getSelectedRowModel().rows.map((r) => r.original);
  const filteredCount = table.getFilteredRowModel().rows.length;
  const totalPages = table.getPageCount();
  const page = table.getState().pagination.pageIndex;

  return (
    <div className={cn('space-y-3', className)}>
      {/* Toolbar */}
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2 flex-1">
          {searchable && (
            <div className="relative max-w-sm flex-1">
              <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
              <input
                type="text"
                placeholder={searchPlaceholder}
                value={globalFilter}
                onChange={(e) => {
                  table.setGlobalFilter(e.target.value);
                  table.setPageIndex(0);
                }}
                className="w-full h-9 pl-9 pr-8 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              {globalFilter && (
                <button
                  onClick={() => {
                    table.setGlobalFilter('');
                    table.setPageIndex(0);
                  }}
                  className="absolute right-2.5 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
                >
                  <X className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          )}
          {toolbar}
        </div>

        <div className="relative">
          <button
            onClick={() => setShowColumnToggle(!showColumnToggle)}
            className="inline-flex items-center gap-1.5 h-9 px-3 rounded-md border border-border
              text-sm text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            <SlidersHorizontal className="h-4 w-4" />
            Columns
          </button>

          {showColumnToggle && (
            <div className="absolute right-0 top-full mt-1 w-48 rounded-md border border-border bg-popover p-1 shadow-lg z-50">
              {columns.map((col) => {
                const column = table.getColumn(col.key);
                const isVisible = column?.getIsVisible() ?? true;
                return (
                  <label
                    key={col.key}
                    className="flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent cursor-pointer"
                  >
                    <input
                      type="checkbox"
                      checked={isVisible}
                      onChange={() => column?.toggleVisibility()}
                      className="rounded border-border text-primary focus:ring-ring"
                    />
                    {col.header}
                  </label>
                );
              })}
            </div>
          )}
        </div>
      </div>

      {selectable && selectedRows.length > 0 && bulkActions ? (
        <div
          className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-muted/30 px-3 py-2 text-sm"
          aria-live="polite"
        >
          <span className="text-muted-foreground">
            {selectedRows.length} {selectedRows.length === 1 ? 'row' : 'rows'} selected
          </span>
          <div className="flex flex-wrap items-center gap-2">
            {bulkActions(selectedRows)}
          </div>
        </div>
      ) : null}

      {/* Table */}
      <div className="rounded-lg border border-border overflow-hidden">
        <div className="overflow-x-auto">
          <Table className="w-full text-sm">
            <TableHeader>
              <TableRow className="border-b border-border bg-muted/50">
                {selectable && (
                  <TableHead className={cn('w-10', selectPadding)}>
                    <input
                      type="checkbox"
                      checked={table.getIsAllPageRowsSelected()}
                      onChange={table.getToggleAllPageRowsSelectedHandler()}
                      className="rounded border-border text-primary focus:ring-ring"
                    />
                  </TableHead>
                )}
                {activeColumns.map((col) => {
                  const column = table.getColumn(col.key);
                  const sorted = column?.getIsSorted();
                  return (
                    <TableHead
                      key={col.key}
                      className={cn(
                        cellPadding,
                        'font-medium text-muted-foreground whitespace-nowrap',
                        col.align === 'center' && 'text-center',
                        col.align === 'right' && 'text-right',
                        col.sortable !== false && 'cursor-pointer select-none hover:text-foreground'
                      )}
                      style={col.width ? { width: col.width } : undefined}
                      onClick={() => col.sortable !== false && column?.toggleSorting()}
                    >
                      <div
                        className={cn(
                          'flex items-center gap-1',
                          col.align === 'center' && 'justify-center',
                          col.align === 'right' && 'justify-end'
                        )}
                      >
                        {col.header}
                        {col.sortable !== false && (
                          <span className="text-muted-foreground/50">
                            {sorted === 'asc' ? (
                              <ChevronUp className="h-3.5 w-3.5" />
                            ) : sorted === 'desc' ? (
                              <ChevronDown className="h-3.5 w-3.5" />
                            ) : (
                              <ChevronsUpDown className="h-3 w-3" />
                            )}
                          </span>
                        )}
                      </div>
                    </TableHead>
                  );
                })}
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                Array.from({ length: skeletonRows }).map((_, i) => (
                  <TableRow key={i} className="border-b border-border last:border-0">
                    {selectable && <TableCell className={selectPadding}><div className="h-4 w-4 rounded bg-muted animate-pulse" /></TableCell>}
                    {activeColumns.map((col) => (
                      <TableCell key={col.key} className={cellPadding}>
                        <div
                          className="h-4 w-24 max-w-full rounded bg-muted animate-pulse"
                          style={{ width: col.width ? `min(100%, ${col.width})` : undefined }}
                        />
                      </TableCell>
                    ))}
                  </TableRow>
                ))
              ) : rows.length === 0 ? (
                <TableRow>
                  <TableCell
                    colSpan={activeColumns.length + (selectable ? 1 : 0)}
                    className="px-4 py-12 text-center text-muted-foreground"
                  >
                    {emptyMessage}
                  </TableCell>
                </TableRow>
              ) : (
                rows.map((row) => {
                  const key = keyExtractor(row.original);
                  const isSelected = row.getIsSelected();
                  return (
                    <TableRow
                      key={key}
                      className={cn(
                        'border-b border-border last:border-0 transition-colors',
                        onRowClick && 'cursor-pointer hover:bg-muted/50',
                        isSelected && 'bg-muted/30'
                      )}
                      onClick={() => onRowClick?.(row.original)}
                    >
                      {selectable && (
                        <TableCell className={selectPadding} onClick={(e) => e.stopPropagation()}>
                          <input
                            type="checkbox"
                            checked={isSelected}
                            onChange={row.getToggleSelectedHandler()}
                            className="rounded border-border text-primary focus:ring-ring"
                          />
                        </TableCell>
                      )}
                      {activeColumns.map((col) => (
                        <TableCell
                          key={col.key}
                          className={cn(
                            cellPadding,
                            col.align === 'center' && 'text-center',
                            col.align === 'right' && 'text-right'
                          )}
                        >
                          {col.accessor(row.original)}
                        </TableCell>
                      ))}
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </div>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            Showing {page * pageSize + 1}-{Math.min((page + 1) * pageSize, filteredCount)} of{' '}
            {filteredCount}
          </span>
          <div className="flex items-center gap-1">
            <button
              onClick={() => table.previousPage()}
              disabled={!table.getCanPreviousPage()}
              className="inline-flex items-center justify-center h-8 w-8 rounded-md border border-border
                text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-50
                disabled:pointer-events-none transition-colors"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            {Array.from({ length: Math.min(totalPages, 5) }).map((_, i) => {
              const pageNum = totalPages <= 5 ? i : Math.max(0, Math.min(page - 2, totalPages - 5)) + i;
              return (
                <button
                  key={pageNum}
                  onClick={() => table.setPageIndex(pageNum)}
                  className={cn(
                    'inline-flex items-center justify-center h-8 w-8 rounded-md text-sm transition-colors',
                    pageNum === page
                      ? 'bg-primary text-primary-foreground'
                      : 'text-muted-foreground hover:text-foreground hover:bg-accent'
                  )}
                >
                  {pageNum + 1}
                </button>
              );
            })}
            <button
              onClick={() => table.nextPage()}
              disabled={!table.getCanNextPage()}
              className="inline-flex items-center justify-center h-8 w-8 rounded-md border border-border
                text-muted-foreground hover:text-foreground hover:bg-accent disabled:opacity-50
                disabled:pointer-events-none transition-colors"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
