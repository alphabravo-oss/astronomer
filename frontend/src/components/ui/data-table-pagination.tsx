import type { Table as RtTable, RowData } from "@tanstack/react-table";
import type { DataTableFeatures } from "./data-table-features";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { cn } from "@/lib/utils";

interface DataTablePaginationProps<T extends RowData> {
  table: RtTable<DataTableFeatures, T>;
  page: number;
  pageSize: number;
  pageCount: number;
  rowCount: number;
  rowCountIsLowerBound?: boolean;
  countUnavailable?: boolean;
  currentRowCount: number;
}

export function DataTablePagination<T extends RowData>({
  table,
  page,
  pageSize,
  pageCount,
  rowCount,
  rowCountIsLowerBound = false,
  countUnavailable = false,
  currentRowCount,
}: DataTablePaginationProps<T>) {
  if (pageCount <= 1 && page === 0) return null;

  const firstPage = Math.max(0, Math.min(page - 2, pageCount - 5));
  const visiblePages = Array.from(
    { length: Math.min(pageCount, 5) },
    (_, index) => (pageCount <= 5 ? index : firstPage + index),
  );

  return (
    <div className="flex items-center justify-between text-sm">
      <span className="text-muted-foreground">
        {countUnavailable ? (
          "Row count unavailable"
        ) : (
          <>
            Showing {currentRowCount === 0 ? 0 : page * pageSize + 1}-
            {currentRowCount === 0 ? 0 : page * pageSize + currentRowCount} of{" "}
            {rowCountIsLowerBound ? "at least " : ""}
            {rowCount.toLocaleString()}
          </>
        )}
      </span>
      <div className="flex items-center gap-1">
        <button
          type="button"
          aria-label="Previous page"
          onClick={() => table.previousPage()}
          disabled={!table.getCanPreviousPage()}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border
            text-muted-foreground transition-colors hover:bg-accent hover:text-foreground
            disabled:pointer-events-none disabled:opacity-50"
        >
          <ChevronLeft className="h-4 w-4" />
        </button>
        {visiblePages.map((pageNumber) => (
          <button
            type="button"
            key={pageNumber}
            aria-label={`Page ${pageNumber + 1}`}
            aria-current={pageNumber === page ? "page" : undefined}
            onClick={() => table.setPageIndex(pageNumber)}
            className={cn(
              "inline-flex h-8 w-8 items-center justify-center rounded-md text-sm transition-colors",
              pageNumber === page
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            {pageNumber + 1}
          </button>
        ))}
        <button
          type="button"
          aria-label="Next page"
          onClick={() => table.nextPage()}
          disabled={!table.getCanNextPage()}
          className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border
            text-muted-foreground transition-colors hover:bg-accent hover:text-foreground
            disabled:pointer-events-none disabled:opacity-50"
        >
          <ChevronRight className="h-4 w-4" />
        </button>
      </div>
    </div>
  );
}
