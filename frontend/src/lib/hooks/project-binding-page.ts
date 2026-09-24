import { useQuery } from "@tanstack/react-query";
import { getBindingPage } from "@/lib/api/rbac-binding-page";
import { queryKeys } from "@/lib/query-keys";
import { usePermissionDecision } from "@/lib/permission-hooks";

export function useProjectBindingPage(projectId: string, pageIndex = 0) {
  const read = usePermissionDecision("rbac", "read", {
    type: "project",
    id: projectId,
  });
  const query = useQuery({
    queryKey: queryKeys.rbac.projectBindingPage(projectId, pageIndex),
    queryFn: ({ signal }) =>
      getBindingPage("project", pageIndex * 25, signal, projectId),
    enabled: !!projectId && read.allowed,
    throwOnError: false,
  });
  // Permission withdrawal must hide previously cached rows and counts as well.
  return {
    data: read.allowed && !query.isError ? query.data : undefined,
    isLoading: read.allowed && query.isLoading,
    isFetching: read.allowed && query.isFetching,
    isError: !read.allowed || query.isError,
    error: read.allowed ? query.error : { status: 403 },
    refetch: query.refetch,
  };
}
