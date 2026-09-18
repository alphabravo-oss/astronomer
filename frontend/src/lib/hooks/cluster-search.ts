import { useInfiniteQuery } from "@tanstack/react-query";
import { getClusters } from "@/lib/api/clusters";
import { queryKeys } from "@/lib/query-keys";

/** Bounded server search; page through the authorized estate on demand. */
export function useClusterSearch(search: string, enabled = true) {
  const term = search.trim();
  return useInfiniteQuery({
    queryKey: queryKeys.clusters.search(term),
    initialPageParam: 1,
    queryFn: ({ pageParam, signal }) =>
      getClusters({ search: term, page: pageParam, pageSize: 50 }, signal),
    getNextPageParam: (page) => {
      const { has_more: hasMore, limit, next_offset: nextOffset } =
        page.pagination;
      return hasMore && nextOffset !== null && limit > 0
        ? Math.floor(nextOffset / limit) + 1
        : undefined;
    },
    enabled,
    staleTime: 30_000,
  });
}
