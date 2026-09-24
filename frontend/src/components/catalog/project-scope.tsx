import { useQuery } from "@tanstack/react-query";
import { FolderSearch } from "lucide-react";
import { RemoteProjectPicker } from "@/components/projects/remote-project-picker";
import { QueryStates } from "@/components/ui/query-states";
import { EmptyState } from "@/components/ui/empty-state";
import {
  getClusterProjects,
  getProject,
  getProjects,
} from "@/lib/api/projects";
import { queryKeys } from "@/lib/query-keys";

/** Resolve deep links directly; only auto-select when a bounded page proves uniqueness. */
export function useCatalogProjectScope(
  requestedId: string,
  clusterId?: string,
) {
  const params = { page: 1, pageSize: 2 };
  const defaults = useQuery({
    queryKey: queryKeys.projects.picker(clusterId, params),
    queryFn: ({ signal }) =>
      clusterId
        ? getClusterProjects(clusterId, params, { signal })
        : getProjects(params, { signal }),
    enabled: !requestedId,
    retry: false,
    throwOnError: false,
  });
  const selected = useQuery({
    queryKey: queryKeys.projects.detail(requestedId),
    queryFn: ({ signal }) => getProject(requestedId, { signal }),
    enabled: !!requestedId,
    retry: false,
    throwOnError: false,
  });
  const query = requestedId ? selected : defaults;
  const candidate = requestedId
    ? selected.data
    : defaults.data?.data.length === 1 && !defaults.data.pagination.has_more
      ? defaults.data.data[0]
      : undefined;
  const wrongCluster =
    !!candidate &&
    !!clusterId &&
    candidate.clusterId !== clusterId &&
    !candidate.clusterIds?.includes(clusterId);
  const project = !query.isError && !wrongCluster ? candidate : undefined;
  return { query, project, projectId: project?.id ?? "", wrongCluster };
}

export function CatalogProjectPicker({
  scope,
  value,
  onChange,
  clusterId,
}: {
  scope: ReturnType<typeof useCatalogProjectScope>;
  value: string;
  onChange: (id: string) => void;
  clusterId?: string;
}) {
  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        Project visibility
        <RemoteProjectPicker
          key={clusterId ?? "global"}
          ariaLabel="Catalog project"
          value={value}
          onChange={onChange}
          clusterId={clusterId}
        />
      </div>
      <QueryStates
        query={{
          ...scope.query,
          data: scope.query.data === undefined ? undefined : true,
        }}
        loadingTitle="Loading catalog project"
        errorTitle="Failed to load catalog project"
        permission="projects:read"
        notFound={
          <EmptyState
            icon={FolderSearch}
            title="Project not found"
            description="Select another project to continue."
            actionLabel="Clear project selection"
            onAction={() => onChange("")}
          />
        }
      >
        {scope.wrongCluster ? (
          <p role="alert" className="text-sm text-destructive">
            This project does not belong to this cluster. Select another
            project.
          </p>
        ) : null}
      </QueryStates>
    </div>
  );
}
