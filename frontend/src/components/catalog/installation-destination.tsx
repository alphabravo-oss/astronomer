import { useQuery } from "@tanstack/react-query";
import { getCluster } from "@/lib/api/clusters";
import { queryKeys } from "@/lib/query-keys";
import { useProject } from "@/lib/hooks/projects";

interface ReleaseDestination {
  namespace: string;
  releaseName: string;
  version?: string;
}
interface DestinationProps {
  clusterId: string;
  projectId?: string;
  manager: "Applications" | "Tools";
  releases: ReleaseDestination[];
}

/** Keep identity visible even when display-name lookups are unavailable. */
export function InstallationDestination({
  clusterId,
  projectId,
  manager,
  releases,
}: DestinationProps) {
  const cluster = useQuery({
    queryKey: queryKeys.clusters.detail(clusterId),
    queryFn: ({ signal }) => getCluster(clusterId, signal),
    enabled: !!clusterId,
    throwOnError: false,
    retry: false,
  });
  const project = useProject(projectId ?? "", {
    throwOnError: false,
    retry: false,
  });
  return (
    <InstallationDestinationView
      clusterId={clusterId}
      clusterName={cluster.data?.displayName || cluster.data?.name}
      projectId={projectId}
      projectName={project.data?.name}
      manager={manager}
      releases={releases}
    />
  );
}

export function InstallationDestinationView({
  clusterId,
  clusterName,
  projectId,
  projectName,
  manager,
  releases,
}: DestinationProps & { clusterName?: string; projectName?: string }) {
  return (
    <section
      aria-label="Installation destination"
      className="space-y-2 break-words rounded-md border border-border bg-muted/20 p-3 text-xs"
    >
      <p className="font-semibold">Installation destination</p>
      <p>
        Cluster: {clusterName ? `${clusterName} · ` : ""}
        {clusterId || "Select a target cluster"}
      </p>
      <p>
        {projectId
          ? `Project context: ${projectName ? `${projectName} · ` : ""}${projectId}`
          : manager === "Tools"
            ? "Ownership: cluster-managed platform capability"
            : "Project context: select a project"}
      </p>
      <ul className="space-y-1">
        {releases.map((release, index) => (
          <li key={`${index}:${release.namespace}:${release.releaseName}`}>
            Namespace:{" "}
            <strong>{release.namespace || "Select a namespace"}</strong> ·
            Release: {release.releaseName || "Enter a release name"}
            {release.version ? ` · Version: ${release.version}` : ""}
          </li>
        ))}
      </ul>
      <p>Managed through {manager}.</p>
      <p className="text-muted-foreground">
        Operators, CRDs and cluster roles can affect the entire cluster.
        Namespace placement does not limit all chart permissions. Namespace
        ownership determines the delivery project.
      </p>
    </section>
  );
}
