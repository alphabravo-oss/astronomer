import { useState } from "react";
import { useClusters } from "@/lib/hooks/clusters";
import { pageTableCount } from "@/lib/api/pagination";
import { useSearchParam } from "@/lib/use-search-param";

/** Bounded estate pages; remote search always resets the server offset. */
export function useClusterEstateTable() {
  const [pageIndex, setPageIndex] = useState(0);
  const [search, setSearch] = useSearchParam("q");
  const pageSize = 25;
  const query = useClusters({ page: pageIndex + 1, pageSize, search });
  return {
    query,
    pageSize,
    serverSide: {
      ...pageTableCount(query.data),
      pagination: { pageIndex, pageSize },
      onPaginationChange: (next: { pageIndex: number }) =>
        setPageIndex(next.pageIndex),
      search: {
        value: search,
        onChange: (value: string) => {
          setSearch(value);
          setPageIndex(0);
        },
      },
    },
  };
}
