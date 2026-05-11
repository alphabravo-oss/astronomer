'use client';

import { useState, useMemo, useCallback } from 'react';
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
  searchable?: boolean;
  searchPlaceholder?: string;
  selectable?: boolean;
  onRowClick?: (row: T) => void;
  onSelectionChange?: (selected: T[]) => void;
  pageSize?: number;
  emptyMessage?: string;
  loading?: boolean;
  toolbar?: React.ReactNode;
  className?: string;
}

// ============================================================
// DataTable Component
// ============================================================

export function DataTable<T>({
  data,
  columns,
  keyExtractor,
  searchable = true,
  searchPlaceholder = 'Search...',
  selectable = false,
  onRowClick,
  onSelectionChange,
  pageSize = 20,
  emptyMessage = 'No results found',
  loading = false,
  toolbar,
  className,
}: DataTableProps<T>) {
  const [search, setSearch] = useState('');
  const [sortKey, setSortKey] = useState<string | null>(null);
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc');
  const [page, setPage] = useState(0);
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set());
  const [visibleColumns, setVisibleColumns] = useState<Set<string>>(
    new Set(columns.filter((c) => !c.hidden).map((c) => c.key))
  );
  const [showColumnToggle, setShowColumnToggle] = useState(false);

  const activeColumns = useMemo(
    () => columns.filter((c) => visibleColumns.has(c.key)),
    [columns, visibleColumns]
  );

  // Filter by search
  const filtered = useMemo(() => {
    if (!search.trim()) return data;
    const q = search.toLowerCase();
    return data.filter((row) =>
      activeColumns.some((col) => {
        const val = col.accessor(row);
        return val?.toString().toLowerCase().includes(q);
      })
    );
  }, [data, search, activeColumns]);

  // Sort
  const sorted = useMemo(() => {
    if (!sortKey) return filtered;
    const col = columns.find((c) => c.key === sortKey);
    if (!col) return filtered;
    const accessor = col.sortAccessor || ((row: T) => {
      const val = col.accessor(row);
      return val?.toString() || '';
    });
    return [...filtered].sort((a, b) => {
      const aVal = accessor(a);
      const bVal = accessor(b);
      if (typeof aVal === 'number' && typeof bVal === 'number') {
        return sortDir === 'asc' ? aVal - bVal : bVal - aVal;
      }
      const aStr = String(aVal);
      const bStr = String(bVal);
      return sortDir === 'asc' ? aStr.localeCompare(bStr) : bStr.localeCompare(aStr);
    });
  }, [filtered, sortKey, sortDir, columns]);

  // Paginate
  const totalPages = Math.ceil(sorted.length / pageSize);
  const paginated = useMemo(
    () => sorted.slice(page * pageSize, (page + 1) * pageSize),
    [sorted, page, pageSize]
  );

  const handleSort = useCallback(
    (key: string) => {
      if (sortKey === key) {
        setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
      } else {
        setSortKey(key);
        setSortDir('asc');
      }
    },
    [sortKey]
  );

  const toggleSelection = useCallback(
    (key: string, row: T) => {
      setSelectedKeys((prev) => {
        const next = new Set(prev);
        if (next.has(key)) {
          next.delete(key);
        } else {
          next.add(key);
        }
        if (onSelectionChange) {
          const selectedRows = data.filter((r) => next.has(keyExtractor(r)));
          onSelectionChange(selectedRows);
        }
        return next;
      });
    },
    [data, keyExtractor, onSelectionChange]
  );

  const toggleAllSelection = useCallback(() => {
    setSelectedKeys((prev) => {
      if (prev.size === paginated.length) {
        onSelectionChange?.([]);
        return new Set();
      }
      const allKeys = paginated.map(keyExtractor);
      onSelectionChange?.(paginated);
      return new Set(allKeys);
    });
  }, [paginated, keyExtractor, onSelectionChange]);

  const toggleColumn = useCallback((key: string) => {
    setVisibleColumns((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        if (next.size > 1) next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
  }, []);

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
                value={search}
                onChange={(e) => {
                  setSearch(e.target.value);
                  setPage(0);
                }}
                className="w-full h-9 pl-9 pr-8 rounded-md border border-border bg-background text-sm
                  placeholder:text-muted-foreground focus:outline-none focus:ring-1 focus:ring-ring"
              />
              {search && (
                <button
                  onClick={() => setSearch('')}
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
              {columns.map((col) => (
                <label
                  key={col.key}
                  className="flex items-center gap-2 px-2 py-1.5 rounded text-sm hover:bg-accent cursor-pointer"
                >
                  <input
                    type="checkbox"
                    checked={visibleColumns.has(col.key)}
                    onChange={() => toggleColumn(col.key)}
                    className="rounded border-border text-primary focus:ring-ring"
                  />
                  {col.header}
                </label>
              ))}
            </div>
          )}
        </div>
      </div>

      {/* Table */}
      <div className="rounded-lg border border-border overflow-hidden">
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border bg-muted/50">
                {selectable && (
                  <th className="w-10 px-3 py-3">
                    <input
                      type="checkbox"
                      checked={selectedKeys.size === paginated.length && paginated.length > 0}
                      onChange={toggleAllSelection}
                      className="rounded border-border text-primary focus:ring-ring"
                    />
                  </th>
                )}
                {activeColumns.map((col) => (
                  <th
                    key={col.key}
                    className={cn(
                      'px-4 py-3 font-medium text-muted-foreground whitespace-nowrap',
                      col.align === 'center' && 'text-center',
                      col.align === 'right' && 'text-right',
                      col.sortable !== false && 'cursor-pointer select-none hover:text-foreground'
                    )}
                    style={col.width ? { width: col.width } : undefined}
                    onClick={() => col.sortable !== false && handleSort(col.key)}
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
                          {sortKey === col.key ? (
                            sortDir === 'asc' ? (
                              <ChevronUp className="h-3.5 w-3.5" />
                            ) : (
                              <ChevronDown className="h-3.5 w-3.5" />
                            )
                          ) : (
                            <ChevronsUpDown className="h-3 w-3" />
                          )}
                        </span>
                      )}
                    </div>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {loading ? (
                Array.from({ length: 5 }).map((_, i) => (
                  <tr key={i} className="border-b border-border last:border-0">
                    {selectable && <td className="px-3 py-3"><div className="h-4 w-4 rounded bg-muted animate-pulse" /></td>}
                    {activeColumns.map((col) => (
                      <td key={col.key} className="px-4 py-3">
                        <div className="h-4 w-24 rounded bg-muted animate-pulse" />
                      </td>
                    ))}
                  </tr>
                ))
              ) : paginated.length === 0 ? (
                <tr>
                  <td
                    colSpan={activeColumns.length + (selectable ? 1 : 0)}
                    className="px-4 py-12 text-center text-muted-foreground"
                  >
                    {emptyMessage}
                  </td>
                </tr>
              ) : (
                paginated.map((row) => {
                  const key = keyExtractor(row);
                  return (
                    <tr
                      key={key}
                      className={cn(
                        'border-b border-border last:border-0 transition-colors',
                        onRowClick && 'cursor-pointer hover:bg-muted/50',
                        selectedKeys.has(key) && 'bg-muted/30'
                      )}
                      onClick={() => onRowClick?.(row)}
                    >
                      {selectable && (
                        <td className="px-3 py-3" onClick={(e) => e.stopPropagation()}>
                          <input
                            type="checkbox"
                            checked={selectedKeys.has(key)}
                            onChange={() => toggleSelection(key, row)}
                            className="rounded border-border text-primary focus:ring-ring"
                          />
                        </td>
                      )}
                      {activeColumns.map((col) => (
                        <td
                          key={col.key}
                          className={cn(
                            'px-4 py-3',
                            col.align === 'center' && 'text-center',
                            col.align === 'right' && 'text-right'
                          )}
                        >
                          {col.accessor(row)}
                        </td>
                      ))}
                    </tr>
                  );
                })
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            Showing {page * pageSize + 1}-{Math.min((page + 1) * pageSize, sorted.length)} of{' '}
            {sorted.length}
          </span>
          <div className="flex items-center gap-1">
            <button
              onClick={() => setPage((p) => Math.max(0, p - 1))}
              disabled={page === 0}
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
                  onClick={() => setPage(pageNum)}
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
              onClick={() => setPage((p) => Math.min(totalPages - 1, p + 1))}
              disabled={page === totalPages - 1}
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
