import type { PaginatedResponse, PaginationMetadata } from "@/types";

export interface CanonicalPageWire<T> {
  data: T[];
  pagination: PaginationMetadata;
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
