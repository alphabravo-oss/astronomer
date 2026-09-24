import type { PaginatedResponse, PaginationMetadata } from "@/types";

export interface CanonicalPageWire<T> {
  data: T[];
  pagination: PaginationMetadata;
}

/** Only for APIs explicitly contracted to return a complete, unpaged array. */
export function completePage<T>(data: T[]): PaginatedResponse<T> {
  return {
    data,
    pagination: {
      total: data.length,
      limit: data.length,
      offset: 0,
      has_more: false,
      next_offset: null,
    },
  };
}

export function mapPage<TWire, TView>(
  page: CanonicalPageWire<TWire>,
  mapper: (item: TWire) => TView,
): PaginatedResponse<TView> {
  return {
    data: page.data.map(mapper),
    pagination: page.pagination,
  };
}

export function pageRowCount<T>(
  page: PaginatedResponse<T> | undefined,
): number {
  if (!page) return 0;
  const { total, offset, has_more: hasMore } = page.pagination;
  if (total !== undefined) return total;
  return offset + page.data.length + (hasMore ? 1 : 0);
}

/** Keep the navigation sentinel separate from claims about an exact total. */
export function pageTableCount<T>(page: PaginatedResponse<T> | undefined) {
  return {
    rowCount: pageRowCount(page),
    rowCountIsLowerBound:
      page?.pagination.total === undefined && !!page?.pagination.has_more,
    // An offset beyond the end of a shrinking collection proves no total.
    ...(page &&
    page.pagination.total === undefined &&
    page.pagination.offset > 0 &&
    page.data.length === 0 &&
    !page.pagination.has_more
      ? { rowCountIsUnknown: true }
      : {}),
  };
}

export function pageCountLabel<T>(
  page: PaginatedResponse<T> | undefined,
): string {
  if (!page) return "—";
  const { rowCount, rowCountIsLowerBound, rowCountIsUnknown } =
    pageTableCount(page);
  if (rowCountIsUnknown) return "Unknown";
  return `${rowCountIsLowerBound ? "At least " : ""}${rowCount.toLocaleString()}`;
}

export function pageNumber(metadata: PaginationMetadata): number {
  return metadata.limit > 0
    ? Math.floor(metadata.offset / metadata.limit) + 1
    : 1;
}

export function pageCount(metadata: PaginationMetadata): number | undefined {
  return metadata.total === undefined || metadata.limit < 1
    ? undefined
    : Math.max(1, Math.ceil(metadata.total / metadata.limit));
}
