import type { QueryState } from "@/components/ui/query-states";
import type { ClusterToolStatus } from "@/types";
import { apiErrorStatus } from "@/lib/api/errors";

export function toolStatusMetric(
  query: Pick<
    QueryState<ClusterToolStatus[]>,
    "data" | "error" | "isError" | "isLoading"
  >,
) {
  if (query.isError)
    return {
      value: "—",
      subtitle: [401, 403].includes(apiErrorStatus(query.error) ?? -1)
        ? "Tool access denied"
        : "Tool status unavailable",
    };
  if (query.isLoading) return { value: "—", subtitle: "Loading tool status…" };
  if (!query.data?.length)
    return { value: "—", subtitle: "No tool status reported" };
  const installed = query.data.filter(
    (tool) =>
      tool.status === "installed" || tool.status === "installed_unmanaged",
  ).length;
  return {
    value: `${installed}/${query.data.length}`,
    subtitle:
      installed === query.data.length
        ? "Reported installed"
        : `${query.data.length - installed} not installed or unhealthy; open Tools for details`,
  };
}
