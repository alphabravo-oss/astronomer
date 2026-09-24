import { useQueries } from "@tanstack/react-query";
import { getAdminUser } from "@/lib/api/account-security";
import { getCluster } from "@/lib/api/clusters";
import { getProject } from "@/lib/api/projects";
import { usePermissionDecision } from "@/lib/permission-hooks";
import { queryKeys } from "@/lib/query-keys";

const ids = (values: (string | null | undefined)[]) => [
  ...new Set(values.filter((id): id is string => !!id)),
];
const readable = <T>(
  queries: { data?: T; isError: boolean }[],
  allowed: boolean,
): T[] =>
  allowed
    ? queries.flatMap((query) =>
        !query.isError && query.data ? [query.data] : [],
      )
    : [];

/** Resolve only explicitly requested visible/selected IDs, never an estate. */
export function useEntityNames({
  userIds = [],
  clusterIds = [],
  projectIds = [],
}: {
  userIds?: (string | null | undefined)[];
  clusterIds?: (string | null | undefined)[];
  projectIds?: (string | null | undefined)[];
}) {
  const usersRead = usePermissionDecision("users", "read").allowed;
  const clustersRead = usePermissionDecision("clusters", "read").allowed;
  const projectsRead = usePermissionDecision("projects", "read").allowed;
  const users = useQueries({
    queries: ids(userIds).map((id) => ({
      queryKey: queryKeys.users.detail(id),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getAdminUser(id, { signal }),
      enabled: usersRead,
      throwOnError: false,
      retry: false,
    })),
  });
  const clusters = useQueries({
    queries: ids(clusterIds).map((id) => ({
      queryKey: queryKeys.clusters.detail(id),
      queryFn: ({ signal }: { signal: AbortSignal }) => getCluster(id, signal),
      enabled: clustersRead,
      throwOnError: false,
      retry: false,
    })),
  });
  const projects = useQueries({
    queries: ids(projectIds).map((id) => ({
      queryKey: queryKeys.projects.detail(id),
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        getProject(id, { signal }),
      enabled: projectsRead,
      throwOnError: false,
      retry: false,
    })),
  });
  return {
    users: readable(users, usersRead),
    clusters: readable(clusters, clustersRead),
    projects: readable(projects, projectsRead),
  };
}
