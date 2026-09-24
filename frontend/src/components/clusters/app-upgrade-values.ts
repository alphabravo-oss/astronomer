import { useEffect, useEffectEvent, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { getClusterAppValues } from "@/lib/api/cluster-apps";
import { queryKeys } from "@/lib/query-keys";

/** Read persisted values under catalog permission; never upgrade with an empty fallback. */
export function useUpgradeValues(
  id: string,
  onValues: (values: string) => void,
) {
  const hydrated = useRef("");
  const apply = useEffectEvent(onValues);
  const query = useQuery({
    queryKey: queryKeys.catalog.releaseValues(id),
    queryFn: ({ signal }) => getClusterAppValues(id, signal),
    enabled: !!id,
    throwOnError: false,
  });
  useEffect(() => {
    if (
      id &&
      hydrated.current !== id &&
      query.data !== undefined &&
      !query.isError
    ) {
      hydrated.current = id;
      apply(query.data);
    }
  }, [id, query.data, query.isError]);
  return query;
}
